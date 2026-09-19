package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/processmetrics"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewservice"
	"github.com/flexdinesh/servediff/internal/reviewstore"
	"github.com/flexdinesh/servediff/internal/session"
	contract "github.com/flexdinesh/servediff/packages/api"
)

type cachedSnapshot struct {
	at       time.Time
	snapshot review.RepositoryDiff
	err      error
}

type Handler struct {
	session   session.Session
	store     *reviewstore.Store
	review    *reviewservice.Service
	assets    fs.FS
	metrics   *processmetrics.Collector
	mu        sync.Mutex
	snapshots map[review.DiffMode]cachedSnapshot
}

func New(active session.Session, store *reviewstore.Store, service *reviewservice.Service, assets fs.FS) *Handler {
	return &Handler{
		session: active, store: store, review: service, assets: assets,
		metrics:   processmetrics.New(),
		snapshots: make(map[review.DiffMode]cachedSnapshot),
	}
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (handler *Handler) SessionID() string { return handler.session.ID }

func (handler *Handler) snapshot(request *http.Request, mode review.DiffMode, fresh bool) (review.RepositoryDiff, error) {
	if !fresh {
		handler.mu.Lock()
		cached, ok := handler.snapshots[mode]
		handler.mu.Unlock()
		if ok && time.Since(cached.at) < 500*time.Millisecond {
			return cached.snapshot, cached.err
		}
	}
	snapshot, err := handler.session.Source.Snapshot(request.Context(), mode)
	handler.mu.Lock()
	handler.snapshots[mode] = cachedSnapshot{at: time.Now(), snapshot: snapshot, err: err}
	handler.mu.Unlock()
	return snapshot, err
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
		handler.problem(response, diffsource.Error(405, "Method not allowed"))
		return
	}
	handler.serveAsset(response, request)
}

func (handler *Handler) checkRequest(request *http.Request) error {
	if origin := request.Header.Get("Origin"); origin != "" && origin != "http://"+request.Host {
		return diffsource.Error(403, "Cross-origin access denied")
	}
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return diffsource.Error(403, "Cross-site access denied")
	}
	return nil
}

func (handler *Handler) api(response http.ResponseWriter, request *http.Request) (bool, error) {
	pathname := request.URL.Path
	if pathname == "/openapi.yaml" {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return true, diffsource.Error(405, "Method not allowed")
		}
		response.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if request.Method == http.MethodGet {
			_, _ = response.Write(contract.OpenAPI)
		}
		return true, nil
	}
	if !strings.HasPrefix(pathname, "/api/v1/") {
		return false, nil
	}
	if pathname == "/api/v1/metrics" && request.Method == http.MethodGet {
		return true, writeJSON(response, 200, handler.metrics.Collect())
	}
	if pathname == "/api/v1/session" && request.Method == http.MethodGet {
		scopes := handler.session.Capabilities.Diff.Scopes.Values
		mode := review.DiffAll
		if len(scopes) > 0 {
			mode = scopes[0]
		}
		snapshot, err := handler.snapshot(request, mode, false)
		if err != nil {
			return true, err
		}
		return true, writeJSON(response, 200, map[string]any{
			"id": handler.session.ID, "source": handler.session.Source.Kind(), "name": snapshot.Name, "root": snapshot.Root,
			"capabilities": handler.session.Capabilities,
		})
	}
	if pathname == "/api/v1/diffs/current" && request.Method == http.MethodGet {
		mode, err := handler.requestScope(request)
		if err != nil {
			return true, err
		}
		snapshot, err := handler.snapshot(request, mode, false)
		return true, writeResult(response, snapshot, err)
	}
	if route, ok := parseFileRoute(pathname); ok && request.Method == http.MethodGet {
		return true, handler.getFile(response, request, route)
	}
	if (commentRoute(pathname) || agentReviewRoute(pathname)) && !handler.session.Capabilities.Review.Comments.Enabled() {
		return true, session.NotEnabled(session.ReviewComments)
	}
	if pathname == "/api/v1/review/comments" && request.Method == http.MethodGet {
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
			err = diffsource.Error(404, "Comment not found")
		}
		return true, writeResult(response, resolution, err)
	}
	if pathname == "/api/v1/comments" {
		switch request.Method {
		case http.MethodGet:
			return true, writeJSON(response, 200, map[string]any{"comments": handler.store.Comments(handler.session.ID)})
		case http.MethodPost:
			return true, handler.createComment(response, request)
		case http.MethodDelete:
			return true, handler.deleteComments(response, request)
		}
	}
	if pathname == "/api/v1/comments/import" && request.Method == http.MethodPost {
		return true, handler.importComments(response, request)
	}
	if pathname == "/api/v1/comments/export" && request.Method == http.MethodGet {
		return true, handler.exportComments(response, request)
	}
	if commentID, ok := routeValue(pathname, "/api/v1/comments/"); ok {
		switch request.Method {
		case http.MethodPatch:
			return true, handler.updateComment(response, request, commentID)
		case http.MethodDelete:
			deleted, err := handler.store.DeleteComment(handler.session.ID, commentID)
			if err != nil {
				return true, err
			}
			if !deleted {
				return true, diffsource.Error(404, "Comment not found")
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	if pathname == "/api/v1/review-marks" {
		mode, err := handler.requestScope(request)
		if err != nil {
			return true, err
		}
		switch request.Method {
		case http.MethodGet:
			return true, writeJSON(response, 200, map[string]any{"marks": handler.store.Marks(handler.session.ID, mode)})
		case http.MethodDelete:
			if err := handler.store.ClearMarks(handler.session.ID, mode); err != nil {
				return true, err
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	if fileID, ok := routeValue(pathname, "/api/v1/review-marks/"); ok {
		switch request.Method {
		case http.MethodPut:
			return true, handler.putMark(response, request, fileID)
		case http.MethodDelete:
			mode, err := handler.requestScope(request)
			if err == nil {
				err = handler.store.DeleteMark(handler.session.ID, mode, fileID)
			}
			if err != nil {
				return true, err
			}
			response.WriteHeader(http.StatusNoContent)
			return true, nil
		}
	}
	known := pathname == "/api/v1/session" || pathname == "/api/v1/diffs/current" ||
		pathname == "/api/v1/comments" || pathname == "/api/v1/comments/import" ||
		pathname == "/api/v1/comments/export" || pathname == "/api/v1/review/comments" ||
		pathname == "/api/v1/review-marks"
	if _, ok := parseFileRoute(pathname); ok {
		known = true
	}
	if _, ok := routeValue(pathname, "/api/v1/comments/"); ok {
		known = true
	}
	if _, ok := routeValue(pathname, "/api/v1/review-marks/"); ok {
		known = true
	}
	if _, ok := reviewResolveRoute(pathname); ok {
		known = true
	}
	if known {
		return true, diffsource.Error(405, "Method not allowed")
	}
	return true, diffsource.Error(404, "Unknown API route")
}

type fileRoute struct{ diffID, fileID, resource string }

func parseFileRoute(pathname string) (fileRoute, bool) {
	parts := strings.Split(strings.TrimPrefix(pathname, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "diffs" || parts[4] != "files" || (parts[6] != "patch" && parts[6] != "contents") {
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
	if pathname == "/api/v1/comments" || pathname == "/api/v1/comments/import" || pathname == "/api/v1/comments/export" {
		return true
	}
	_, ok := routeValue(pathname, "/api/v1/comments/")
	return ok
}

func agentReviewRoute(pathname string) bool {
	if pathname == "/api/v1/review/comments" {
		return true
	}
	_, ok := reviewResolveRoute(pathname)
	return ok
}

func reviewResolveRoute(pathname string) (string, bool) {
	const prefix = "/api/v1/review/comments/"
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
	return false, diffsource.Error(400, "Invalid %s value", name)
}

func (handler *Handler) requestScope(request *http.Request) (review.DiffMode, error) {
	mode, err := review.ParseDiffMode(request.URL.Query().Get("scope"))
	if err != nil {
		return "", diffsource.Error(400, "Invalid diff scope")
	}
	if !handler.session.Capabilities.Diff.Scopes.Allows(mode) {
		return "", session.NotEnabled(session.DiffScopes)
	}
	return mode, nil
}

func (handler *Handler) currentFile(request *http.Request, mode review.DiffMode, diffID, fileID, fileVersion string, fresh bool) (review.RepositoryDiff, review.ChangedFile, error) {
	snapshot, err := handler.snapshot(request, mode, fresh)
	if err != nil {
		return review.RepositoryDiff{}, review.ChangedFile{}, err
	}
	if snapshot.Revision != diffID {
		return review.RepositoryDiff{}, review.ChangedFile{}, diffsource.Error(409, "Diff changed. Refresh to load the latest version.")
	}
	for _, file := range snapshot.Files {
		if file.ID != fileID {
			continue
		}
		if file.Fingerprint != fileVersion {
			return review.RepositoryDiff{}, review.ChangedFile{}, diffsource.Error(409, "File changed. Refresh to load the latest version.")
		}
		return snapshot, file, nil
	}
	return review.RepositoryDiff{}, review.ChangedFile{}, diffsource.Error(404, "File is not in the current diff")
}

func (handler *Handler) getFile(response http.ResponseWriter, request *http.Request, route fileRoute) error {
	if route.resource == "contents" && !handler.session.Capabilities.Files.Contents.Enabled() {
		return session.NotEnabled(session.FilesContents)
	}
	mode, err := handler.requestScope(request)
	if err != nil {
		return err
	}
	snapshot, file, err := handler.currentFile(request, mode, route.diffID, route.fileID, request.URL.Query().Get("fileVersion"), false)
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

type createCommentRequest struct {
	DiffID      string          `json:"diffId"`
	FileID      string          `json:"fileId"`
	Scope       review.DiffMode `json:"scope"`
	FileVersion string          `json:"fileVersion"`
	Side        string          `json:"side"`
	Start       int             `json:"start"`
	End         int             `json:"end"`
	Body        string          `json:"body"`
}

func (handler *Handler) createComment(response http.ResponseWriter, request *http.Request) error {
	var body createCommentRequest
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if _, err := review.ParseDiffMode(string(body.Scope)); err != nil || (body.Side != "additions" && body.Side != "deletions") || body.Start < 1 || body.End < 1 || strings.TrimSpace(body.Body) == "" {
		return diffsource.Error(400, "Invalid comment")
	}
	if !handler.session.Capabilities.Diff.Scopes.Allows(body.Scope) {
		return session.NotEnabled(session.DiffScopes)
	}
	snapshot, file, err := handler.currentFile(request, body.Scope, body.DiffID, body.FileID, body.FileVersion, true)
	if err != nil {
		return err
	}
	preview, err := handler.session.Source.Patch(request.Context(), body.Scope, file, snapshot.Head)
	if err != nil {
		return err
	}
	code, ok := diffsource.PatchContext(preview.Patch, body.Side, body.Start, body.End)
	if !ok && preview.Contents != nil {
		contents := preview.Contents.After
		if body.Side == "deletions" {
			contents = preview.Contents.Before
		}
		code, ok = contentContext(contents, body.Start, body.End)
	}
	if !ok {
		return diffsource.Error(400, "Select up to 200 visible lines on one side")
	}
	start, end := body.Start, body.End
	if start > end {
		start, end = end, start
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	comment := review.ReviewComment{
		ID: id, Path: file.Path, Scope: body.Scope, Fingerprint: file.Fingerprint,
		Side: body.Side, Start: start, End: end, Code: code, Body: strings.TrimSpace(body.Body), Status: "open", CreatedAt: float64(time.Now().UnixMilli()),
		Origin: &review.ReviewOrigin{Source: snapshot.Source, Repository: snapshot.Name, Branch: snapshot.Branch, Head: snapshot.Head, Revision: snapshot.Revision, File: review.ReviewFileOrigin{Status: file.Status, OldPath: file.OldPath}},
	}
	if err := handler.store.PutComment(handler.session.ID, comment); err != nil {
		return err
	}
	return writeJSON(response, http.StatusCreated, comment)
}

func contentContext(contents string, start, end int) (string, bool) {
	if start > end {
		start, end = end, start
	}
	if start < 1 || end-start >= 200 {
		return "", false
	}
	lines := strings.Split(contents, "\n")
	if end > len(lines) {
		return "", false
	}
	selected := make([]string, 0, end-start+1)
	for line := start; line <= end; line++ {
		selected = append(selected, "  "+strings.TrimSuffix(lines[line-1], "\r"))
	}
	return strings.Join(selected, "\n"), true
}

func (handler *Handler) repositories(request *http.Request, comments []review.ReviewComment) ([]review.RepositoryDiff, error) {
	needed := make(map[review.DiffMode]bool)
	allowed := make(map[review.DiffMode]bool)
	for _, scope := range handler.session.Capabilities.Diff.Scopes.Values {
		allowed[scope] = true
	}
	for _, comment := range comments {
		needed[comment.Scope] = allowed[comment.Scope]
	}
	result := make([]review.RepositoryDiff, 0, len(needed))
	for mode, include := range needed {
		if !include {
			continue
		}
		snapshot, err := handler.snapshot(request, mode, false)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func repositoryFor(comment review.ReviewComment, repositories []review.RepositoryDiff) *review.RepositoryDiff {
	for index := range repositories {
		if repositories[index].Mode == comment.Scope {
			return &repositories[index]
		}
	}
	return nil
}

func (handler *Handler) deleteComments(response http.ResponseWriter, request *http.Request) error {
	selection := request.URL.Query().Get("status")
	if selection != "all" && selection != "open" && selection != "resolved" && selection != "stale" {
		return diffsource.Error(400, "Invalid comment status")
	}
	comments := handler.store.Comments(handler.session.ID)
	repositories, err := handler.repositories(request, comments)
	if err != nil {
		return err
	}
	selected := make(map[string]bool)
	for _, comment := range comments {
		stale := review.Applicability(comment, repositoryFor(comment, repositories)) == "stale"
		if selection == "all" || (selection == "stale" && stale) || (!stale && comment.Status == selection) {
			selected[comment.ID] = true
		}
	}
	deleted, err := handler.store.DeleteComments(handler.session.ID, selected)
	if err != nil {
		return err
	}
	return writeJSON(response, 200, map[string]any{"comments": handler.store.Comments(handler.session.ID), "deleted": deleted})
}

func (handler *Handler) importComments(response http.ResponseWriter, request *http.Request) error {
	var body struct {
		Comments *[]review.ReviewComment `json:"comments"`
	}
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if body.Comments == nil {
		return diffsource.Error(400, "Invalid comment import")
	}
	for _, comment := range *body.Comments {
		if !comment.Valid() {
			return diffsource.Error(400, "Invalid comment import")
		}
	}
	comments, err := handler.store.ImportComments(handler.session.ID, *body.Comments)
	if err != nil {
		return err
	}
	return writeJSON(response, 200, map[string]any{"comments": comments})
}

func (handler *Handler) exportComments(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	resolved := query.Get("includeResolved")
	if query.Has("includeResolved") && resolved != "true" && resolved != "false" {
		return diffsource.Error(400, "Invalid includeResolved value")
	}
	revision, requestedScope := query.Get("revision"), query.Get("scope")
	if query.Has("scope") {
		parsed, err := review.ParseDiffMode(requestedScope)
		if err != nil {
			return diffsource.Error(400, "Invalid diff scope")
		}
		if !handler.session.Capabilities.Diff.Scopes.Allows(parsed) {
			return session.NotEnabled(session.DiffScopes)
		}
	}
	if query.Has("revision") != query.Has("scope") {
		return diffsource.Error(400, "revision and scope must be provided together")
	}
	var mode review.DiffMode
	var current *review.RepositoryDiff
	if requestedScope != "" {
		var err error
		mode, err = review.ParseDiffMode(requestedScope)
		if err != nil {
			return diffsource.Error(400, "Invalid diff scope")
		}
		snapshot, err := handler.snapshot(request, mode, false)
		if err != nil {
			return err
		}
		current = &snapshot
	}
	selected := make([]review.ReviewComment, 0)
	for _, comment := range handler.store.Comments(handler.session.ID) {
		if id := query.Get("commentId"); id != "" && comment.ID != id {
			continue
		}
		if revision != "" {
			matches := comment.Scope == mode && comment.Origin != nil && comment.Origin.Revision == revision
			if comment.Origin == nil && current != nil && current.Revision == revision {
				for _, file := range current.Files {
					matches = matches || (file.Path == comment.Path && file.Fingerprint == comment.Fingerprint)
				}
			}
			if !matches {
				continue
			}
		}
		selected = append(selected, comment)
	}
	repositories, err := handler.repositories(request, selected)
	if err != nil {
		return err
	}
	response.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, err = io.WriteString(response, review.FormatComments(selected, resolved == "true", repositories))
	return err
}

func (handler *Handler) updateComment(response http.ResponseWriter, request *http.Request, commentID string) error {
	var body struct {
		Body   *string `json:"body"`
		Status *string `json:"status"`
	}
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if body.Body == nil && body.Status == nil || body.Body != nil && strings.TrimSpace(*body.Body) == "" || body.Status != nil && *body.Status != "open" && *body.Status != "resolved" {
		return diffsource.Error(400, "Invalid comment update")
	}
	comments := handler.store.Comments(handler.session.ID)
	for _, comment := range comments {
		if comment.ID != commentID {
			continue
		}
		if body.Body != nil {
			comment.Body = strings.TrimSpace(*body.Body)
		}
		if body.Status != nil {
			comment.Status = *body.Status
		}
		if err := handler.store.PutComment(handler.session.ID, comment); err != nil {
			return err
		}
		return writeJSON(response, 200, comment)
	}
	return diffsource.Error(404, "Comment not found")
}

func (handler *Handler) putMark(response http.ResponseWriter, request *http.Request, fileID string) error {
	mode, err := handler.requestScope(request)
	if err != nil {
		return err
	}
	var body struct {
		FileVersion *string `json:"fileVersion"`
	}
	if err := decodeBody(response, request, &body); err != nil {
		return err
	}
	if body.FileVersion == nil {
		return diffsource.Error(400, "Invalid review mark")
	}
	snapshot, err := handler.snapshot(request, mode, true)
	if err != nil {
		return err
	}
	for _, file := range snapshot.Files {
		if file.ID != fileID {
			continue
		}
		if file.Fingerprint != *body.FileVersion {
			return diffsource.Error(409, "File changed. Refresh to try again.")
		}
		mark := review.ReviewMark{FileID: fileID, FileVersion: *body.FileVersion, Scope: mode}
		if err := handler.store.PutMark(handler.session.ID, mark); err != nil {
			return err
		}
		return writeJSON(response, 200, mark)
	}
	return diffsource.Error(404, "File is not in the current diff")
}

func decodeBody(response http.ResponseWriter, request *http.Request, target any) error {
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		return diffsource.Error(415, "Expected application/json request body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return diffsource.Error(413, "Request body exceeds 1 MiB")
		}
		return diffsource.Error(400, "Invalid JSON request body")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return diffsource.Error(400, "Invalid JSON request body")
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
		handler.problem(response, diffsource.Error(404, "Not found"))
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		handler.problem(response, diffsource.Error(404, "Not found"))
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
	var requestError *diffsource.RequestError
	if errors.As(err, &requestError) {
		status = requestError.Status
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
