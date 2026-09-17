/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
	"github.com/antoniocali/polaris-k8s/internal/polaris/catalog"
)

// PolarisTableReconciler reconciles a PolarisTable.
//
// Reconciliation in this initial pass covers table create + delete only.
// Schema and partition spec drift detection requires constructing Iceberg
// CommitTable updates which is non-trivial — left as a follow-up. Once a
// table exists, the operator currently only enforces drift on tableable
// properties; schema changes must be applied out-of-band.
type PolarisTableReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polaristables,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polaristables/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polaristables/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces;polariscatalogs,verbs=get;list;watch

func (r *PolarisTableReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	tbl := &polarisv1alpha1.PolarisTable{}
	if err := r.Get(ctx, req.NamespacedName, tbl); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// On the delete path, a failure to resolve the connection chain or build
	// the client means the remote is unreachable (the PolarisConnection or its
	// credentials Secret was deleted first — common during namespace teardown).
	// Treat that as "remote already gone" and drop the finalizer rather than
	// wedging the object in Terminating forever.
	deleting := !tbl.DeletionTimestamp.IsZero()

	ns, err := getNamespace(ctx, r.Client, tbl.Spec.NamespaceRef, tbl.Namespace)
	if err != nil {
		return r.resolveFail(ctx, tbl, deleting, ReasonRefError, err)
	}
	// Wait quietly until the parent namespace is reconciled.
	if !deleting && !parentReady(ns.Status.Conditions) {
		return waitForDependency(ctx, r.Client, tbl, &tbl.Status.Conditions, tbl.Generation, "PolarisNamespace "+ns.Name)
	}
	path, cat, err := resolveNamespacePath(ctx, r.Client, ns)
	if err != nil {
		return r.resolveFail(ctx, tbl, deleting, ReasonRefError, err)
	}
	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		return r.resolveFail(ctx, tbl, deleting, ReasonRefError, err)
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		return r.resolveFail(ctx, tbl, deleting, ReasonAuthError, err)
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)
	tableName := polarisName(tbl.Spec.Name, tbl.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, tbl, pc, catalogName, path, tableName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, tbl); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	loadResp, err := pc.Catalog.LoadTableWithResponse(ctx, catalogName, encodeNamespacePath(path), tableName, &catalog.LoadTableParams{})
	if err != nil {
		setNotReady(&tbl.Status.Conditions, tbl.Generation, ReasonPolarisError, err.Error())
		tbl.Status.ObservedGeneration = tbl.Generation
		if updErr := r.Status().Update(ctx, tbl); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("load table: %w", err)
	}

	if loadResp.StatusCode() == http.StatusNotFound {
		body, err := r.buildCreateBody(tbl, tableName)
		if err != nil {
			setNotReady(&tbl.Status.Conditions, tbl.Generation, ReasonInvalidSpec, err.Error())
			tbl.Status.ObservedGeneration = tbl.Generation
			if updErr := r.Status().Update(ctx, tbl); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		resp, err := pc.Catalog.CreateTableWithResponse(ctx, catalogName, encodeNamespacePath(path), &catalog.CreateTableParams{}, *body)
		if err != nil {
			setNotReady(&tbl.Status.Conditions, tbl.Generation, ReasonPolarisError, err.Error())
			tbl.Status.ObservedGeneration = tbl.Generation
			if updErr := r.Status().Update(ctx, tbl); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, fmt.Errorf("create table: %w", err)
		}
		// A 404 here means the parent namespace isn't on the server yet — the
		// generated client reports it via StatusCode, not err. Wait quietly.
		if resp.StatusCode() == http.StatusNotFound {
			return waitForDependency(ctx, r.Client, tbl, &tbl.Status.Conditions, tbl.Generation, "namespace (Polaris-side)")
		}
		if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
			err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "tables/create", Body: string(resp.Body)}
			setNotReady(&tbl.Status.Conditions, tbl.Generation, ReasonPolarisError, err.Error())
			tbl.Status.ObservedGeneration = tbl.Generation
			if updErr := r.Status().Update(ctx, tbl); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		if resp.JSON200 != nil && resp.JSON200.Metadata.Location != nil {
			tbl.Status.Location = *resp.JSON200.Metadata.Location
		}
	} else if loadResp.StatusCode() >= 400 {
		err := &polaris.APIError{StatusCode: loadResp.StatusCode(), Op: "tables/load", Body: string(loadResp.Body)}
		setNotReady(&tbl.Status.Conditions, tbl.Generation, ReasonPolarisError, err.Error())
		tbl.Status.ObservedGeneration = tbl.Generation
		if updErr := r.Status().Update(ctx, tbl); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	} else if loadResp.JSON200 != nil {
		// Surface observed metadata even on no-op.
		if loadResp.JSON200.Metadata.Location != nil {
			tbl.Status.Location = *loadResp.JSON200.Metadata.Location
		}
		tbl.Status.TableUUID = loadResp.JSON200.Metadata.TableUuid
	}

	tbl.Status.ObservedGeneration = tbl.Generation
	setSynced(&tbl.Status.Conditions, tbl.Generation, "table state matches spec")
	setReady(&tbl.Status.Conditions, tbl.Generation, "table is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, tbl)
}

func (r *PolarisTableReconciler) finalize(ctx context.Context, tbl *polarisv1alpha1.PolarisTable, pc *polaris.Client, catalogName string, path []string, tableName string) error {
	resp, err := pc.Catalog.DropTableWithResponse(ctx, catalogName, encodeNamespacePath(path), tableName, &catalog.DropTableParams{})
	if err != nil {
		return fmt.Errorf("drop table: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "tables/drop", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, tbl)
}

func (r *PolarisTableReconciler) buildCreateBody(tbl *polarisv1alpha1.PolarisTable, name string) (*catalog.CreateTableJSONRequestBody, error) {
	schema, err := buildIcebergSchema(tbl.Spec.Schema)
	if err != nil {
		return nil, err
	}
	body := &catalog.CreateTableJSONRequestBody{
		Name:   name,
		Schema: schema,
	}
	if tbl.Spec.PartitionSpec != nil {
		ps := buildPartitionSpec(*tbl.Spec.PartitionSpec)
		body.PartitionSpec = &ps
	}
	if len(tbl.Spec.Properties) > 0 || tbl.Spec.WriteFormat != "" {
		props := copyStringMap(tbl.Spec.Properties)
		if tbl.Spec.WriteFormat != "" {
			props["write.format.default"] = string(tbl.Spec.WriteFormat)
		}
		body.Properties = &props
	}
	return body, nil
}

// buildIcebergSchema maps our IcebergSchema → catalog.Schema. Only top-level
// primitive types are handled; users wanting nested struct/list/map should
// emit the JSON inline via a future raw-body escape hatch.
func buildIcebergSchema(s polarisv1alpha1.IcebergSchema) (catalog.Schema, error) {
	out := catalog.Schema{Type: "struct"}
	for _, f := range s.Fields {
		var ft catalog.Type
		if err := ft.MergePrimitiveType(f.Type); err != nil {
			return catalog.Schema{}, fmt.Errorf("schema field %q has unsupported type %q (only primitive types supported in v1alpha1): %w", f.Name, f.Type, err)
		}
		field := catalog.StructField{
			Id:       int(f.ID),
			Name:     f.Name,
			Required: f.Required,
			Type:     ft,
		}
		if f.Doc != "" {
			doc := f.Doc
			field.Doc = &doc
		}
		out.Fields = append(out.Fields, field)
	}
	if len(s.IdentifierFieldIds) > 0 {
		ids := make([]int, 0, len(s.IdentifierFieldIds))
		for _, v := range s.IdentifierFieldIds {
			ids = append(ids, int(v))
		}
		out.IdentifierFieldIds = &ids
	}
	return out, nil
}

func buildPartitionSpec(ps polarisv1alpha1.IcebergPartitionSpec) catalog.PartitionSpec {
	out := catalog.PartitionSpec{}
	for _, f := range ps.Fields {
		out.Fields = append(out.Fields, catalog.PartitionField{
			SourceId:  int(f.SourceID),
			FieldId:   new(int(f.FieldID)),
			Name:      f.Name,
			Transform: f.Transform,
		})
	}
	return out
}

// resolveFail handles a pre-deletion-check resolution error (ref lookup or
// client build). On the delete path the remote is unreachable, so we drop the
// finalizer rather than wedging the object in Terminating; otherwise we record
// the failure in status and return the error for requeue.
func (r *PolarisTableReconciler) resolveFail(ctx context.Context, tbl *polarisv1alpha1.PolarisTable, deleting bool, reason string, err error) (ctrl.Result, error) {
	if deleting {
		return ctrl.Result{}, removeFinalizer(ctx, r.Client, tbl)
	}
	setNotReady(&tbl.Status.Conditions, tbl.Generation, reason, err.Error())
	tbl.Status.ObservedGeneration = tbl.Generation
	if updErr := r.Status().Update(ctx, tbl); updErr != nil {
		logf.FromContext(ctx).Error(updErr, "update status")
	}
	return ctrl.Result{}, err
}

func (r *PolarisTableReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisTableReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisTable{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polaristable").
		Complete(r)
}
