/*
Copyright 2025 SAP SE.
*/

package controller

import (
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/clients/xsql"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/controller/auditpolicy"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/controller/dbschema"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/controller/role"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/controller/user"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/controller/usergroup"
)

// Setup creates all HANA controllers with the supplied logger and adds
// them to the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	for _, setup := range []func(ctrl.Manager, controller.Options, xsql.DB) error{
		role.Setup,
		usergroup.Setup,
		dbschema.Setup,
		auditpolicy.Setup,
		user.Setup,
	} {
		if err := setup(mgr, o, db); err != nil {
			return err
		}
	}
	return nil
}
