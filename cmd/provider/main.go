/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package main

import (
	"os"
	"path/filepath"
	"time"

	kingpin "github.com/alecthomas/kingpin/v2"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"

	"github.com/SAP/crossplane-provider-hana/apis"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	hanaController "github.com/SAP/crossplane-provider-hana/internal/controller"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

func main() {
	var (
		app            = kingpin.New(filepath.Base(os.Args[0]), "hana support for Crossplane.").DefaultEnvars()
		debug          = app.Flag("debug", "Run with debug logging.").Short('d').Bool()
		leaderElection = app.Flag("leader-election", "Use leader election for the controller manager.").Short('l').Default("false").OverrideDefaultFromEnvar("LEADER_ELECTION").Bool()

		syncInterval     = app.Flag("sync", "How often all resources will be double-checked for drift from the desired state.").Short('s').Default("1h").Duration()
		pollInterval     = app.Flag("poll", "How often individual resources will be checked for drift from the desired state").Default("1m").Duration()
		maxReconcileRate = app.Flag("max-reconcile-rate", "The global maximum rate per second at which resources may checked for drift from the desired state.").Default("10").Int()
	)
	kingpin.MustParse(app.Parse(os.Args[1:]))

	// Configure logging with klog
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	log := logging.NewLogrLogger(ctrl.Log.WithName("provider-hana"))

	log.Info("Starting provider-hana", "debug", *debug)

	cfg, err := ctrl.GetConfig()
	kingpin.FatalIfError(err, "Cannot get API server rest config")

	mgr, err := ctrl.NewManager(ratelimiter.LimitRESTConfig(cfg, *maxReconcileRate), ctrl.Options{
		Cache: cache.Options{
			SyncPeriod: syncInterval,
		},

		// controller-runtime uses both ConfigMaps and Leases for leader
		// election by default. Leases expire after 15 seconds, with a
		// 10 second renewal deadline. We've observed leader loss due to
		// renewal deadlines being exceeded when under high load - i.e.
		// hundreds of reconciles per second and ~200rps to the API
		// server. Switching to Leases only and longer leases appears to
		// alleviate this.
		LeaderElection:             *leaderElection,
		LeaderElectionID:           "crossplane-leader-election-provider-hana",
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		LeaseDuration:              func() *time.Duration { d := 60 * time.Second; return &d }(),
		RenewDeadline:              func() *time.Duration { d := 50 * time.Second; return &d }(),
	})
	kingpin.FatalIfError(err, "Cannot create controller manager")
	kingpin.FatalIfError(apis.AddToScheme(mgr.GetScheme()), "Cannot add hana APIs to scheme")

	o := controller.Options{
		Logger:                  log,
		MaxConcurrentReconciles: *maxReconcileRate,
		PollInterval:            *pollInterval,
		GlobalRateLimiter:       ratelimiter.NewGlobal(*maxReconcileRate),
		Features:                &feature.Flags{},
	}

	o.Features.Enable(features.EnableAlphaManagementPolicies)
	log.Info("Beta feature enabled by default", "flag", features.EnableAlphaManagementPolicies)

	hanaDB := hana.New(log.WithValues("component", "hanaDB"))
	defer hanaDB.Disconnect() //nolint:errcheck

	kingpin.FatalIfError(hanaController.Setup(mgr, o, hanaDB), "Cannot setup hana controllers")
	kingpin.FatalIfError(mgr.Start(ctrl.SetupSignalHandler()), "Cannot start controller manager")
}
