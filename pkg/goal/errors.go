package goal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"

	"charm.land/fantasy"
)

// TurnErrorClass is the goal runtime's verdict on a failed agent turn:
// whether retrying the same objective later could plausibly succeed, or
// whether the failure is structural and a human must look at it first.
type TurnErrorClass int

const (
	// TurnErrorPermanent marks a failure that will not resolve by trying
	// again (bad request, auth failure, context overflow). The runtime
	// pauses the goal and asks the user to review.
	TurnErrorPermanent TurnErrorClass = iota
	// TurnErrorTransient marks a failure worth retrying with backoff
	// (rate limits, 5xx, network flaps, malformed-stream blips).
	TurnErrorTransient
	// TurnErrorCancelled marks a run the user (or host) canceled. The
	// runtime pauses the goal rather than fighting the cancel; the user
	// resumes deliberately when ready.
	TurnErrorCancelled
)

// ClassifyTurnError decides how the goal runtime should react to an error
// that ended a turn. It is deliberately conservative toward "transient" for
// unknown error types: the consecutive-failure cap bounds retries, so a
// misclassified permanent error still ends in a paused goal rather than a
// dead session.
func ClassifyTurnError(err error) TurnErrorClass {
	if err == nil {
		return TurnErrorPermanent
	}

	// A canceled run - user Escape, session cancel, or shutdown. Check
	// first: providers frequently wrap the cancellation in their own
	// error types.
	if errors.Is(err, context.Canceled) {
		return TurnErrorCancelled
	}
	// A run that outlived its deadline is worth another attempt.
	if errors.Is(err, context.DeadlineExceeded) {
		return TurnErrorTransient
	}

	var providerErr *fantasy.ProviderError
	if errors.As(err, &providerErr) {
		// Context overflow will not improve on retry; the session needs
		// compaction or a human decision.
		if providerErr.IsContextTooLarge() || providerErr.ContextTooLargeErr {
			return TurnErrorPermanent
		}
		// Auth failures need the user to re-login (or a token refresh the
		// coordinator already attempted); looping retries is pointless.
		if providerErr.AuthError || providerErr.StatusCode == http.StatusUnauthorized {
			return TurnErrorPermanent
		}
		// 501 and 505 describe the server's capabilities, not a
		// temporary condition. Check them before fantasy's generic
		// retryable rule, which treats everything in 5xx as retryable.
		if providerErr.StatusCode == http.StatusNotImplemented ||
			providerErr.StatusCode == http.StatusHTTPVersionNotSupported {
			return TurnErrorPermanent
		}
		if providerErr.IsRetryable() || providerErr.TransientError {
			return TurnErrorTransient
		}
		switch {
		case providerErr.StatusCode == http.StatusRequestTimeout ||
			providerErr.StatusCode == http.StatusTooManyRequests:
			return TurnErrorTransient
		case providerErr.StatusCode == 0:
			// Transport wrapper (NewTransportError): no HTTP status at
			// all, so this is a connect/read failure.
			return TurnErrorTransient
		case providerErr.StatusCode >= 500:
			return TurnErrorTransient
		}
		return TurnErrorPermanent
	}

	// Non-provider errors are overwhelmingly I/O failures from the stream
	// or shell layers.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return TurnErrorTransient
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return TurnErrorTransient
	}

	// Unknown: give it the bounded retry budget rather than declaring
	// defeat; the consecutive-failure cap keeps this safe.
	return TurnErrorTransient
}
