//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
2025 Copyright metal-stack Authors.
*/

// Code generated by conversion-gen. DO NOT EDIT.

package v1alpha1

import (
	unsafe "unsafe"

	ontap "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap"
	conversion "k8s.io/apimachinery/pkg/conversion"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

func init() {
	localSchemeBuilder.Register(RegisterConversions)
}

// RegisterConversions adds conversion functions to the given scheme.
// Public to allow building arbitrary schemes.
func RegisterConversions(s *runtime.Scheme) error {
	if err := s.AddGeneratedConversionFunc((*TridentConfig)(nil), (*ontap.TridentConfig)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha1_TridentConfig_To_ontap_TridentConfig(a.(*TridentConfig), b.(*ontap.TridentConfig), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*ontap.TridentConfig)(nil), (*TridentConfig)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_ontap_TridentConfig_To_v1alpha1_TridentConfig(a.(*ontap.TridentConfig), b.(*TridentConfig), scope)
	}); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1alpha1_TridentConfig_To_ontap_TridentConfig(in *TridentConfig, out *ontap.TridentConfig, s conversion.Scope) error {
	out.SVMName = in.SVMName
	out.Protocols = *(*ontap.Protocols)(unsafe.Pointer(&in.Protocols))
	out.SVMSecretRef = in.SVMSecretRef
	out.DataLif = in.DataLif
	out.ManagementLif = in.ManagementLif
	return nil
}

// Convert_v1alpha1_TridentConfig_To_ontap_TridentConfig is an autogenerated conversion function.
func Convert_v1alpha1_TridentConfig_To_ontap_TridentConfig(in *TridentConfig, out *ontap.TridentConfig, s conversion.Scope) error {
	return autoConvert_v1alpha1_TridentConfig_To_ontap_TridentConfig(in, out, s)
}

func autoConvert_ontap_TridentConfig_To_v1alpha1_TridentConfig(in *ontap.TridentConfig, out *TridentConfig, s conversion.Scope) error {
	out.SVMName = in.SVMName
	out.Protocols = *(*Protocols)(unsafe.Pointer(&in.Protocols))
	out.SVMSecretRef = in.SVMSecretRef
	out.DataLif = in.DataLif
	out.ManagementLif = in.ManagementLif
	return nil
}

// Convert_ontap_TridentConfig_To_v1alpha1_TridentConfig is an autogenerated conversion function.
func Convert_ontap_TridentConfig_To_v1alpha1_TridentConfig(in *ontap.TridentConfig, out *TridentConfig, s conversion.Scope) error {
	return autoConvert_ontap_TridentConfig_To_v1alpha1_TridentConfig(in, out, s)
}
