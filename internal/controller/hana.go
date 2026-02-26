/*
Copyright 2026 SAP SE.
*/

package controller

import (
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/auditpolicy"
	"github.com/SAP/crossplane-provider-hana/internal/controller/dbschema"
	"github.com/SAP/crossplane-provider-hana/internal/controller/instancemapping"
	"github.com/SAP/crossplane-provider-hana/internal/controller/kymainstancemapping"
	"github.com/SAP/crossplane-provider-hana/internal/controller/personalsecurityenvironment"
	"github.com/SAP/crossplane-provider-hana/internal/controller/role"
	"github.com/SAP/crossplane-provider-hana/internal/controller/user"
	"github.com/SAP/crossplane-provider-hana/internal/controller/usergroup"
	"github.com/SAP/crossplane-provider-hana/internal/controller/x509provider"
)

// Setup creates all HANA controllers with the supplied logger and adds
// them to the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	// SQL-based controllers
	for _, setup := range []func(ctrl.Manager, controller.Options, xsql.DB) error{
		role.Setup,
		usergroup.Setup,
		dbschema.Setup,
		auditpolicy.Setup,
		user.Setup,
		x509provider.Setup,
		personalsecurityenvironment.Setup,
	} {
		if err := setup(mgr, o, db); err != nil {
			return err
		}
	}
	// Non SQL controllers
	if err := instancemapping.Setup(mgr, o); err != nil {
		return err
	}
	if err := kymainstancemapping.Setup(mgr, o); err != nil {
		return err
	}

	return nil
}
