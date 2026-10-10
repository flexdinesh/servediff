package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/flexdinesh/diffx/internal/processmetrics"
	"github.com/flexdinesh/diffx/internal/review"
	"github.com/flexdinesh/diffx/internal/reviewdata"
	"github.com/flexdinesh/diffx/internal/reviewservice"
	"github.com/flexdinesh/diffx/internal/session"
	contract "github.com/flexdinesh/diffx/packages/api"
)

type Handler struct {
	session session.Session
	store   Store
	review  *reviewservice.Service
	assets  fs.FS
	metrics *processmetrics.Collector
}

func New(active session.Session, store Store, service *reviewservice.Service, assets fs.FS) *Handler {
	return &Handler{
		session: active, store: store, review: service, assets: assets,
		metrics: processmetrics.New(),
	}
}

func (handler *Handler) SessionID() string { return handler.session.ID }

func (handler *Handler) ownsDiff(diffID string) bool {
	for _, id := range handler.session.DiffIDs {
		if id == diffID {
			return true
		}
	}
	return false
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Cache-Control", "no-store")
	if err := handler.checkRequest(request); err != nil {
		handler.problem(response, err)
		return
	}
	handled, err := handler.api(response, request)
	if err != nil {
		handler.problem(response, err)
		return
	}
	if handled {
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		handler.problem(response, review.Error(405, "Method not allowed"))
		return
	}
	handler.serveAsset(response, request)
}

func (handler *Handler) checkRequest(request *http.Request) error {
	if origin := request.Header.Get("Origin"); origin != "" && origin != "http://"+request.Host && origin != "https://"+request.Host {
		return review.Error(403, "Cross-origin access denied")
	}
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return review.Error(403, "Cross-site access denied")
	}
	return nil
}

func (handler *Handler) api(response http.ResponseWriter, request *http.Request) (bool, error) {
	pathname := request.URL.Path
	if pathname == "/openapi.yaml" {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return true, review.Error(405, "Method not allowed")
		}
		response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if request.Method == http.MethodGet {
			_, _ = response.Write(contract.OpenAPI)
		}
		return true, nil
	}
	if !strings.HasPrefix(pathname, "/api/review/") {
		return false, nil
	}
	if pathname == "/api/review/metrics" && request.Method == http.MethodGet {
		return true, writeJSON(response, 200, handler.metrics.Collect())
	}
	if pathname == "/api/review/session" && request.Method == http.MethodGet {
		scopes := handler.session.Capabilities.Diff.Scopes.Values
		mode := review.DiffAll
		if len(scopes) > 0 {
			mode = scopes[0]
		}
		snapshot, err := handler.review.Snapshot(request.Context(), mode)
		if err != nil {
			return true, err
		}
		return true, writeJSON(response, 200, map[string]any{
			"id": handler.session.ID, "source": handler.session.Source.Kind(), "name": snapshot.Name, "root": snapshot.Root,
			"user": handler.session.User, "locationId": handler.session.LocationID, "repositoryId": handler.session.RepositoryID,
			"capabilities": handler.session.Capabilities,
		})
	}
	if pathname == "/api/review/diffs/current" && request.Method == http.MethodGet {
		mode, err := handler.requestScope(request)
		if err != nil {
			return true, err
		}
		snapshot, err := handler.review.Snapshot(request.Context(), mode)
		return true, writeResult(response, snapshot, err)
	}
	if diffID, versionID, ok := parseVersionRoute(pathname); ok && request.Method == http.MethodGet {
		if !handler.ownsDiff(diffID) {
			return true, review.Error(404, "Diff version not found")
		}
		snapshot, err := handler.store.StoredVersion(handler.session.User.ID, diffID, versionID, time.Now())
		if errors.Is(err, reviewdata.ErrNotFound) {
			err = review.Error(404, "Diff version not found")
		}
		return true, writeResult(response, snapshot, err)
	}
	if route, ok := parseFileRoute(pathname); ok && request.Method == http.MethodGet {
		return true, handler.getFile(response, request, route)
	}
	if (commentRoute(pathname) || agentReviewRoute(pathname)) && !handler.session.Capabilities.Review.Comments.Enabled() {
		return true, session.NotEnabled(session.ReviewComments)
	}
	if pathname == "/api/review/review/comments" && request.Method == http.MethodGet {
		includeResolved, err := booleanQuery(request, "includeResolved")
		if err != nil {
			return true, err
		}
		comments, err := handler.review.ListComments(request.Context(), includeResolved)
		return true, writeResult(response, map[string]any{"comments": comments}, err)
	}
	if commentID, ok := reviewResolveRoute(pathname); ok && request.Method == http.MethodPost {
		resolution, err := handler.review.ResolveComment(commentID)
		if errors.Is(err, reviewservice.ErrCommentNotFound) {
			err = review.Error(404, "Comment not found")
		}
		return true, writeResult(response, resolution, err)
	}
	if pathname == "/api/review/comments" {
		switch request.Method {
		case http.MethodGet:
			comments, err := handler.review.Comments()
			return true, writeResult(response, map[string]any{"comments": comments}, err)
		case http.MethodPost:
			return true, handler.createComment(response, request)
		case http.MethodDelete:
			return true, handler.deleteComments(response, request)
		}
	}
	if pathname == "/api/review/comments/import" && request.Method == http.MethodPost {
		return true, handler.importComments(response, request)
	}
	if pathname == "/api/review/comments/export" && request.Method == http.MethodGet {
		return true, handler.exportComments(response, request)
	}
	if commentID, ok := routeValue(pathname, "/api/review/comments/"); ok {
		switch request.Method {
		case http.MethodPatch:
			return true, handler.updateComment(response, request, commentID)
		case http.MethodDelete:
			deleted, err := handler.review.DeleteComment(commentID)
			if err != nil {
				return true, err
			}
			if !deleted {
				return true, review.Error(404, "Comment not found")
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	if pathname == "/api/review/review-marks" {
		mode, err := handler.requestScope(request)
		if err != nil {
			return true, err
		}
		switch request.Method {
		case http.MethodGet:
			marks, err := handler.review.Marks(mode)
			return true, writeResult(response, map[string]any{"marks": marks}, err)
		case http.MethodDelete:
			if err := handler.review.ClearMarks(mode); err != nil {
				return true, err
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	if fileID, ok := routeValue(pathname, "/api/review/review-marks/"); ok {
		switch request.Method {
		case http.MethodPut:
			return true, handler.putMark(response, request, fileID)
		case http.MethodDelete:
			mode, err := handler.requestScope(request)
			if err == nil {
				err = handler.review.DeleteMark(mode, fileID)
			}
			if err != nil {
				return true, err
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	known := pathname == "/api/review/session" || pathname == "/api/review/diffs/current" ||
		pathname == "/api/review/comments" || pathname == "/api/review/comments/import" ||
		pathname == "/api/review/comments/export" || pathname == "/api/review/review/comments" ||
		pathname == "/api/review/review-marks"
	if _, ok := parseFileRoute(pathname); ok {
		known = true
	}
	if _, _, ok := parseVersionRoute(pathname); ok {
		known = true
	}
	if _, ok := routeValue(pathname, "/api/review/comments/"); ok {
		known = true
	}
	if _, ok := routeValue(pathname, "/api/review/review-marks/"); ok {
		known = true
	}
	if _, ok := reviewResolveRoute(pathname); ok {
		known = true
	}
	if known {
		return true, review.Error(405, "Method not allowed")
	}
	return true, review.Error(404, "Unknown API route")
}

type fileRoute struct{ diffID, fileID, resource string }

func parseVersionRoute(pathname string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(pathname, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "review" || parts[2] != "diffs" || parts[4] != "versions" || parts[3] == "" || parts[5] == "" {
		return "", "", false
	}
	return parts[3], parts[5], true
}

func parseFileRoute(pathname string) (fileRoute, bool) {
	parts := strings.Split(strings.TrimPrefix(pathname, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[1] != "review" || parts[2] != "diffs" || parts[4] != "files" || (parts[6] != "patch" && parts[6] != "contents") {
		return fileRoute{}, false
	}
	return fileRoute{diffID: parts[3], fileID: parts[5], resource: parts[6]}, true
}

func routeValue(pathname, prefix string) (string, bool) {
	if !strings.HasPrefix(pathname, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(pathname, prefix)
	return value, value != "" && !strings.Contains(value, "/")
}

func commentRoute(pathname string) bool {
	if pathname == "/api/review/comments" || pathname == "/api/review/comments/import" || pathname == "/api/review/comments/export" {
		return true
	}
	_, ok := routeValue(pathname, "/api/review/comments/")
	return ok
}

func agentReviewRoute(pathname string) bool {
	if pathname == "/api/review/review/comments" {
		return true
	}
	_, ok := reviewResolveRoute(pathname)
	return ok
}

func reviewResolveRoute(pathname string) (string, bool) {
	const prefix = "/api/review/review/comments/"
	const suffix = "/resolve"
	if !strings.HasPrefix(pathname, prefix) || !strings.HasSuffix(pathname, suffix) {
		return "", false
	}
	commentID := strings.TrimSuffix(strings.TrimPrefix(pathname, prefix), suffix)
	return commentID, commentID != "" && !strings.Contains(commentID, "/")
}

func booleanQuery(request *http.Request, name string) (bool, error) {
	value := request.URL.Query().Get(name)
	if !request.URL.Query().Has(name) || value == "false" {
		return false, nil
	}
	if value == "true" {
		return true, nil
	}
	return false, review.Error(400, "Invalid %s value", name)
}

func (handler *Handler) requestScope(request *http.Request) (review.DiffMode, error) {
	mode, err := review.ParseDiffMode(request.URL.Query().Get("scope"))
	if err != nil {
		return "", review.Error(400, "Invalid diff scope")
	}
	if !handler.session.Capabilities.Diff.Scopes.Allows(mode) {
		return "", session.NotEnabled(session.DiffScopes)
	}
	return mode, nil
}

func (handler *Handler) currentFile(request *http.Request, mode review.DiffMode, diffID, versionID, fileID, fileVersion string) (review.RepositoryDiff, review.ChangedFile, error) {
	return handler.review.CurrentFile(request.Context(), mode, diffID, versionID, fileID, fileVersion)
}

func (handler *Handler) getFile(response http.ResponseWriter, request *http.Request, route fileRoute) error {
	if !handler.ownsDiff(route.diffID) {
		return review.Error(404, "File preview not found")
	}
	if route.resource == "contents" && !handler.session.Capabilities.Files.Contents.Enabled() {
		return session.NotEnabled(session.FilesContents)
	}
	mode, err := handler.requestScope(request)
	if err != nil {
		return err
	}
	versionID := request.URL.Query().Get("versionId")
	if versionID == "" {
		return review.Error(400, "Version ID is required")
	}
	fileVersion := request.URL.Query().Get("fileVersion")
	snapshot, file, err := handler.currentFile(request, mode, route.diffID, versionID, route.fileID, fileVersion)
	if err != nil {
		return err
	}
	if route.resource == "patch" {
		result, err := handler.session.Source.Patch(request.Context(), mode, file, snapshot.Head)
		return writeResult(response, result, err)
	}
	result, err := handler.session.Source.Contents(request.Context(), mode, file, snapshot.Head)
	return writeResult(response, result, err)
}

func (handler *Handler) createComment(response http.ResponseWriter, request *http.Request) error {
	var body reviewservice.CreateCommentInput
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	comment, err := handler.review.CreateComment(request.Context(), body)
	if err != nil {
		return err
	}
	return writeJSON(response, http.StatusCreated, comment)
}

func (handler *Handler) deleteComments(response http.ResponseWriter, request *http.Request) error {
	result, err := handler.review.DeleteComments(request.Context(), request.URL.Query().Get("status"))
	return writeResult(response, result, err)
}

func (handler *Handler) importComments(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Comments *[]review.ReviewComment `json:"comments"`
	}
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if body.Comments == nil {
		return review.Error(400, "Invalid comment import")
	}
	comments, err := handler.review.ImportComments(request.Context(), *body.Comments)
	return writeResult(response, map[string]any{"comments": comments}, err)
}

func (handler *Handler) exportComments(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	resolved := query.Get("includeResolved")
	if query.Has("includeResolved") && resolved != "true" && resolved != "false" {
		return review.Error(400, "Invalid includeResolved value")
	}
	revision, requestedScope := query.Get("revision"), query.Get("scope")
	if query.Has("scope") {
		parsed, err := review.ParseDiffMode(requestedScope)
		if err != nil {
			return review.Error(400, "Invalid diff scope")
		}
		if !handler.session.Capabilities.Diff.Scopes.Allows(parsed) {
			return session.NotEnabled(session.DiffScopes)
		}
	}
	if query.Has("revision") != query.Has("scope") {
		return review.Error(400, "revision and scope must be provided together")
	}
	formatted, err := handler.review.ExportComments(request.Context(), reviewservice.ExportCommentsInput{Revision: revision, Scope: requestedScope, CommentID: query.Get("commentId"), IncludeResolved: resolved == "true"})
	if err != nil {
		return err
	}
	response.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, err = io.WriteString(response, formatted)
	return err
}

func (handler *Handler) updateComment(response http.ResponseWriter, request *http.Request, commentID string) error {
	var body reviewservice.UpdateCommentInput
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	comment, err := handler.review.UpdateComment(commentID, body)
	return writeResult(response, comment, err)
}

func (handler *Handler) putMark(response http.ResponseWriter, request *http.Request, fileID string) error {
	mode, err := handler.requestScope(request)
	if err != nil {
		return err
	}
	var body struct {
		FileVersion *string `json:"fileVersion"`
		VersionID   *string `json:"versionId"`
	}
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if body.FileVersion == nil || body.VersionID == nil {
		return review.Error(400, "Invalid review mark")
	}
	mark, err := handler.review.PutMark(request.Context(), mode, fileID, *body.VersionID, *body.FileVersion)
	return writeResult(response, mark, err)
}

func decodeBody(response http.ResponseWriter, request *http.Request, target any) error {
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		return review.Error(415, "Expected application/json request body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return review.Error(413, "Request body exceeds 1 MiB")
		}
		return review.Error(400, "Invalid JSON request body")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return review.Error(400, "Invalid JSON request body")
	}
	return nil
}

func (handler *Handler) serveAsset(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	file, err := handler.assets.Open(name)
	if err != nil {
		handler.problem(response, review.Error(404, "Not found"))
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		handler.problem(response, review.Error(404, "Not found"))
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	if request.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(response, file)
}

func (handler *Handler) problem(response http.ResponseWriter, err error) {
	status := 500
	body := map[string]any{"type": "about:blank", "title": "Request Failed", "status": status, "detail": err.Error()}
	var requestError *review.RequestError
	if errors.As(err, &requestError) {
		status = requestError.Status
		if status == http.StatusServiceUnavailable && strings.HasPrefix(requestError.Detail, "source_unavailable:") {
			body["code"] = "source_unavailable"
		}
	}
	var capabilityError *session.CapabilityError
	if errors.As(err, &capabilityError) {
		status = http.StatusNotFound
		body["code"] = "capability_not_enabled"
		body["capability"] = capabilityError.Capability
	}
	title := "Request Failed"
	if status >= 500 {
		title = "Internal Server Error"
	}
	body["title"] = title
	body["status"] = status
	_ = writeJSONType(response, status, "application/problem+json; charset=utf-8", body)
}

func writeResult(response http.ResponseWriter, value any, err error) error {
	if err != nil {
		return err
	}
	return writeJSON(response, 200, value)
}

func writeJSON(response http.ResponseWriter, status int, value any) error {
	return writeJSONType(response, status, "application/json; charset=utf-8", value)
}

func writeJSONType(response http.ResponseWriter, status int, contentType string, value any) error {
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(status)
	return json.NewEncoder(response).Encode(value)
}
