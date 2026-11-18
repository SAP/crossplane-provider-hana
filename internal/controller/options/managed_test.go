package options

import (
	"context"
	"testing"

	crossplanecontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type countingReconciler struct {
	calls int
}

func (r *countingReconciler) Reconcile(context.Context, reconcile.Request) (reconcile.Result, error) {
	r.calls++
	return reconcile.Result{}, nil
}

func TestForManagedController(t *testing.T) {
	o := crossplanecontroller.DefaultOptions()
	o.MaxConcurrentReconciles = 7
	o.GlobalRateLimiter = ratelimiter.NewGlobal(1)

	firstInner := &countingReconciler{}
	secondInner := &countingReconciler{}
	first := ForManagedController("first", o, firstInner)
	second := ForManagedController("second", o, secondInner)

	if got := first.RuntimeOptions.MaxConcurrentReconciles; got != 7 {
		t.Fatalf("first controller max concurrent reconciles = %d, want 7", got)
	}
	if got := second.RuntimeOptions.MaxConcurrentReconciles; got != 7 {
		t.Fatalf("second controller max concurrent reconciles = %d, want 7", got)
	}
	if first.RuntimeOptions.RateLimiter == nil || second.RuntimeOptions.RateLimiter == nil {
		t.Fatal("controller-runtime rate limiter was not configured")
	}
	if first.RuntimeOptions.RecoverPanic == nil || !*first.RuntimeOptions.RecoverPanic {
		t.Fatal("controller-runtime panic recovery was not enabled")
	}

	for i := range 10 {
		r := first.Reconciler
		if i%2 == 1 {
			r = second.Reconciler
		}
		if result, err := r.Reconcile(context.Background(), reconcile.Request{}); err != nil || result.RequeueAfter != 0 {
			t.Fatalf("reconcile %d = (%+v, %v), want immediate success", i+1, result, err)
		}
	}
	if firstInner.calls != 5 || secondInner.calls != 5 {
		t.Fatalf("successful reconciles = (%d, %d), want (5, 5)", firstInner.calls, secondInner.calls)
	}

	callsBeforeRateLimited := firstInner.calls
	result, err := first.Reconciler.Reconcile(context.Background(), reconcile.Request{})
	if err != nil {
		t.Fatalf("rate-limited reconcile error = %v, want nil", err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("rate-limited reconcile delay = %s, want positive", result.RequeueAfter)
	}
	if firstInner.calls != callsBeforeRateLimited {
		t.Fatal("rate-limited reconcile reached the inner reconciler")
	}
}
