/*
Copyright 2025 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconcile

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	
	serror "github.com/fluxcd/source-controller/internal/error"
)

// ExponentialBackoffResultBuilder implements a RuntimeResultBuilder that uses
// exponential backoff for errors and a fixed interval for successful reconciliations.
type ExponentialBackoffResultBuilder struct {
	// RequeueAfter is the fixed period at which the reconciler requeues on
	// successful execution.
	RequeueAfter time.Duration
	// MinBackoff is the minimum backoff duration for errors.
	MinBackoff time.Duration
	// MaxBackoff is the maximum backoff duration for errors.
	MaxBackoff time.Duration
	// FailureCount tracks consecutive failures for exponential backoff.
	// This should be stored per-resource in the reconciler.
	FailureCount int
}

// BuildRuntimeResult converts a given Result and error into the
// return values of a controller's Reconcile function with exponential backoff for errors.
func (r ExponentialBackoffResultBuilder) BuildRuntimeResult(rr Result, err error) ctrl.Result {
	// Handle special errors that contribute to expressing the result.
	switch e := err.(type) {
	case *serror.Waiting:
		// Safeguard: If no RequeueAfter is set, use exponential backoff
		if e.RequeueAfter == 0 {
			return ctrl.Result{RequeueAfter: r.calculateBackoff()}
		}
		return ctrl.Result{RequeueAfter: e.RequeueAfter}
	case *serror.Generic:
		// For non-ignored errors, use exponential backoff
		if !e.Ignore {
			return ctrl.Result{RequeueAfter: r.calculateBackoff()}
		}
		// Ignored errors reset failure count and use success interval
		r.FailureCount = 0
		return ctrl.Result{RequeueAfter: r.RequeueAfter}
	}

	// Handle regular errors with exponential backoff
	if err != nil {
		return ctrl.Result{RequeueAfter: r.calculateBackoff()}
	}

	// Success cases reset failure count
	r.FailureCount = 0

	switch rr {
	case ResultRequeue:
		return ctrl.Result{Requeue: true}
	case ResultSuccess:
		return ctrl.Result{RequeueAfter: r.RequeueAfter}
	default:
		return ctrl.Result{}
	}
}

// calculateBackoff returns the next backoff duration using exponential backoff algorithm.
func (r *ExponentialBackoffResultBuilder) calculateBackoff() time.Duration {
	if r.MinBackoff == 0 {
		r.MinBackoff = 5 * time.Second
	}
	if r.MaxBackoff == 0 {
		r.MaxBackoff = 15 * time.Minute
	}

	// Calculate exponential backoff: min * 2^failures
	backoff := r.MinBackoff * time.Duration(1<<uint(r.FailureCount))
	
	// Cap at maximum backoff
	if backoff > r.MaxBackoff {
		backoff = r.MaxBackoff
	}

	// Increment failure count for next calculation
	r.FailureCount++

	return backoff
}

// IsSuccess returns if a given runtime result is success for this builder.
func (r ExponentialBackoffResultBuilder) IsSuccess(result ctrl.Result) bool {
	return result.RequeueAfter > 0 && result.RequeueAfter <= r.RequeueAfter
}