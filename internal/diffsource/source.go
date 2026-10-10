package diffsource

import "github.com/flexdinesh/diffx/internal/review"

type Source = review.Source
type Support = review.Support
type RequestError = review.RequestError

func Error(status int, format string, values ...any) error {
	return review.Error(status, format, values...)
}
func Message(value string) *string { return review.Message(value) }
