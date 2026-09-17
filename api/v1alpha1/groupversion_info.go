/*
Copyright 2026.

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

// Package v1alpha1 contains API Schema definitions for the polaris v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=polaris.k8s.calific.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// SchemeGroupVersion is group version used to register these objects.
	// This name is used by applyconfiguration generators (e.g. controller-gen).
	SchemeGroupVersion = schema.GroupVersion{Group: "polaris.k8s.calific.io", Version: "v1alpha1"}

	// GroupVersion is an alias for SchemeGroupVersion, for backward compatibility.
	GroupVersion = SchemeGroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &groupVersionSchemeBuilder{}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// groupVersionSchemeBuilder registers types under SchemeGroupVersion using
// only k8s.io/apimachinery. sigs.k8s.io/controller-runtime/pkg/scheme.Builder
// is deprecated for this purpose since api packages should have minimal
// dependencies.
type groupVersionSchemeBuilder struct {
	runtime.SchemeBuilder
}

// Register adds the given types to SchemeGroupVersion.
func (b *groupVersionSchemeBuilder) Register(objects ...runtime.Object) *groupVersionSchemeBuilder {
	b.SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, objects...)
		return nil
	})
	return b
}

// AddToScheme registers SchemeGroupVersion's common types plus everything
// added via Register.
func (b *groupVersionSchemeBuilder) AddToScheme(s *runtime.Scheme) error {
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return b.SchemeBuilder.AddToScheme(s)
}
