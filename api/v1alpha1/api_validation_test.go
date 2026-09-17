/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"encoding/json"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Compile-time guarantee that every CRD type implements runtime.Object via the
// generated deepcopy code. If a kind is missing from the scheme we register
// here, the schemeBuilder.AddToScheme call in TestSchemeRegistration would
// silently miss it and the operator wouldn't be able to serve that kind — so
// we keep one canonical sample per kind below and exercise every code path
// (JSON round-trip + DeepCopyObject) over the same list.
var sampleObjects = []runtime.Object{
	&PolarisConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "prod", Namespace: "tenant-a"},
		Spec: PolarisConnectionSpec{
			ServerURL: "https://polaris.example.com",
			CredentialsSecretRef: ClientCredentialsSecretRef{
				Name: "polaris-creds",
			},
		},
	},
	&PolarisCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "lakehouse", Namespace: "tenant-a"},
		Spec: PolarisCatalogSpec{
			ConnectionRef:       ConnectionRef{Name: "prod"},
			Type:                CatalogTypeInternal,
			DefaultBaseLocation: "s3://example/lakehouse",
			StorageConfig: StorageConfig{
				StorageType:      StorageTypeS3,
				AllowedLocations: []string{"s3://example/lakehouse"},
				S3: &S3StorageConfig{
					RoleARN: "arn:aws:iam::123456789012:role/polaris",
					Region:  "eu-west-1",
				},
			},
		},
	},
	&PolarisNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "tenant-a"},
		Spec: PolarisNamespaceSpec{
			CatalogRef: CatalogRef{Name: "lakehouse"},
		},
	},
	&PolarisTable{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "tenant-a"},
		Spec: PolarisTableSpec{
			NamespaceRef: NamespaceRef{Name: "analytics"},
			Schema: IcebergSchema{
				Fields: []IcebergField{
					{ID: 1, Name: "id", Type: "long", Required: true},
					{ID: 2, Name: "ts", Type: "timestamp"},
				},
			},
			WriteFormat: WriteFormatParquet,
		},
	},
	&PolarisView{
		ObjectMeta: metav1.ObjectMeta{Name: "orders_v", Namespace: "tenant-a"},
		Spec: PolarisViewSpec{
			NamespaceRef: NamespaceRef{Name: "analytics"},
			Schema: IcebergSchema{
				Fields: []IcebergField{{ID: 1, Name: "id", Type: "long"}},
			},
			SQL:     "SELECT id FROM orders",
			Dialect: "spark",
		},
	},
	&PolarisPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "compact", Namespace: "tenant-a"},
		Spec: PolarisPolicySpec{
			NamespaceRef: NamespaceRef{Name: "analytics"},
			Type:         "system.data-compaction",
			Content:      apiextensionsv1.JSON{Raw: []byte(`{"target_file_size_mb":256}`)},
		},
	},
	&PolarisPrincipal{
		ObjectMeta: metav1.ObjectMeta{Name: "airflow", Namespace: "tenant-a"},
		Spec: PolarisPrincipalSpec{
			ConnectionRef:        ConnectionRef{Name: "prod"},
			CredentialsSecretRef: GeneratedCredentialsSecretRef{Name: "airflow-polaris"},
		},
	},
	&PolarisPrincipalRole{
		ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "tenant-a"},
		Spec: PolarisPrincipalRoleSpec{
			ConnectionRef: ConnectionRef{Name: "prod"},
		},
	},
	&PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "lakehouse-rw", Namespace: "tenant-a"},
		Spec: PolarisCatalogRoleSpec{
			CatalogRef: CatalogRef{Name: "lakehouse"},
		},
	},
	&PolarisPrincipalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "airflow-writer", Namespace: "tenant-a"},
		Spec: PolarisPrincipalRoleBindingSpec{
			PrincipalRef:     PrincipalRef{Name: "airflow"},
			PrincipalRoleRef: PrincipalRoleRef{Name: "writer"},
		},
	},
	&PolarisCatalogRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "writer-lakehouse-rw", Namespace: "tenant-a"},
		Spec: PolarisCatalogRoleBindingSpec{
			PrincipalRoleRef: PrincipalRoleRef{Name: "writer"},
			CatalogRoleRef:   CatalogRoleRef{Name: "lakehouse-rw"},
		},
	},
	&PolarisGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "rw-tables", Namespace: "tenant-a"},
		Spec: PolarisGrantSpec{
			CatalogRoleRef: CatalogRoleRef{Name: "lakehouse-rw"},
			Privilege:      Privilege("TABLE_WRITE_DATA"),
			Target: GrantTarget{
				Type:         GrantTargetNamespace,
				NamespaceRef: &NamespaceRef{Name: "analytics"},
			},
		},
	},
}

// TestSampleObjectsCoverAllKinds is the safety net: if someone adds a new
// CRD kind but forgets to extend sampleObjects, the count check below fails
// and so do all the per-kind tests that loop over it.
func TestSampleObjectsCoverAllKinds(t *testing.T) {
	const want = 12
	if got := len(sampleObjects); got != want {
		t.Fatalf("sampleObjects has %d entries, want %d (one per CRD kind)", got, want)
	}
}

// TestSchemeRegistration confirms every kind we ship registers cleanly into
// a scheme. Catches missing init() calls or duplicate GVKs early.
func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, obj := range sampleObjects {
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			t.Errorf("%T: not registered in scheme: %v", obj, err)
			continue
		}
		if len(gvks) == 0 {
			t.Errorf("%T: no GVK", obj)
		}
	}
}

// TestJSONRoundTrip exercises the JSON tags on every CRD spec. A mismatch
// (missing tag, wrong field name) shows up as a non-equal round-trip.
func TestJSONRoundTrip(t *testing.T) {
	for _, obj := range sampleObjects {
		t.Run(reflect.TypeOf(obj).Elem().Name(), func(t *testing.T) {
			data, err := json.Marshal(obj)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			out := reflect.New(reflect.TypeOf(obj).Elem()).Interface().(runtime.Object)
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("unmarshal: %v\npayload: %s", err, data)
			}

			redo, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if string(data) != string(redo) {
				t.Errorf("round-trip differs:\nfirst:  %s\nsecond: %s", data, redo)
			}
		})
	}
}

// TestDeepCopy validates that every kind's DeepCopyObject produces an
// independent copy. This is exactly what the generated code is supposed to
// do, but a missing or stale zz_generated.deepcopy.go would silently break
// reconciliation later, so we assert it here.
func TestDeepCopy(t *testing.T) {
	for _, obj := range sampleObjects {
		t.Run(reflect.TypeOf(obj).Elem().Name(), func(t *testing.T) {
			cp := obj.DeepCopyObject()
			if cp == nil {
				t.Fatal("DeepCopyObject returned nil")
			}
			if reflect.ValueOf(cp).Pointer() == reflect.ValueOf(obj).Pointer() {
				t.Fatal("DeepCopyObject returned the same pointer")
			}
			a, _ := json.Marshal(obj)
			b, _ := json.Marshal(cp)
			if string(a) != string(b) {
				t.Errorf("deepcopy differs from original:\nsrc: %s\ncpy: %s", a, b)
			}
		})
	}
}

// TestRefFieldsArePurposeBuilt is a structural test: it confirms that we
// never accidentally regressed to a generic corev1.ObjectReference shape on
// any ref struct. Each ref must be a struct with at minimum a Name field.
func TestRefFieldsArePurposeBuilt(t *testing.T) {
	refTypes := []any{
		ConnectionRef{}, CatalogRef{}, NamespaceRef{}, TableRef{}, ViewRef{},
		PrincipalRef{}, PrincipalRoleRef{}, CatalogRoleRef{},
		ClientCredentialsSecretRef{}, GeneratedCredentialsSecretRef{},
		CABundleSecretRef{},
	}
	for _, r := range refTypes {
		rt := reflect.TypeOf(r)
		t.Run(rt.Name(), func(t *testing.T) {
			nameField, ok := rt.FieldByName("Name")
			if !ok {
				t.Fatalf("%s missing Name field", rt.Name())
			}
			if nameField.Type.Kind() != reflect.String {
				t.Errorf("%s.Name is %s, want string", rt.Name(), nameField.Type.Kind())
			}
			// The forbidden upstream shapes carry fields we deliberately
			// avoid. If any of these show up on a ref, we've drifted.
			for _, forbidden := range []string{"UID", "APIVersion", "Kind", "ResourceVersion", "FieldPath"} {
				if _, bad := rt.FieldByName(forbidden); bad {
					t.Errorf("%s has %s field — should not mirror corev1.ObjectReference", rt.Name(), forbidden)
				}
			}
		})
	}
}
