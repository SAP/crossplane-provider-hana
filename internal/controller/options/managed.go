package options

import (
	crossplanecontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	runtimecontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ManagedController contains the controller-runtime and reconcile options
// used by managed resource controllers.
type ManagedController struct {
	RuntimeOptions runtimecontroller.Options
	Reconciler     reconcile.Reconciler
}

// ForManagedController configures controller-runtime workers and wraps the
// reconciler with the provider's shared global rate limiter.
func ForManagedController(name string, o crossplanecontroller.Options, r reconcile.Reconciler) ManagedController {
	return ManagedController{
		RuntimeOptions: o.ForControllerRuntime(),
		Reconciler:     ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter),
	}
}
