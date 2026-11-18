package validation

import (
	"net/netip"

	"k8s.io/apimachinery/pkg/util/validation/field"

	ontapv1alpha1 "github.com/metal-stack/gardener-extension-ontap/pkg/apis/ontap/v1alpha1"
)

// ValidateTridentConfig validates a Trident provider configuration.
func ValidateTridentConfig(tridentConfig *ontapv1alpha1.TridentConfig) field.ErrorList {
	allErrs := field.ErrorList{}

	if tridentConfig == nil {
		return append(allErrs, field.Required(field.NewPath(""), "trident configuration must not be nil"))
	}

	svmPath := field.NewPath("svmIpaddresses")

	managementLifPath := svmPath.Child("managementLif")
	switch {
	case tridentConfig.SvmIpaddresses.ManagementLif == "":
		allErrs = append(allErrs, field.Required(managementLifPath, "management LIF IP address must be provided"))
	default:
		if _, err := netip.ParseAddr(tridentConfig.SvmIpaddresses.ManagementLif); err != nil {
			allErrs = append(allErrs, field.Invalid(managementLifPath, tridentConfig.SvmIpaddresses.ManagementLif, "must be a valid IP address"))
		}
	}

	dataLifsPath := svmPath.Child("dataLifs")
	if len(tridentConfig.SvmIpaddresses.DataLifs) == 0 {
		allErrs = append(allErrs, field.Required(dataLifsPath, "data LIF IP addresses must be provided"))
	}
	for i, dataLif := range tridentConfig.SvmIpaddresses.DataLifs {
		dataLifPath := dataLifsPath.Index(i)
		switch {
		case dataLif == "":
			allErrs = append(allErrs, field.Required(dataLifPath, "data LIF IP address must not be empty"))
		default:
			if _, err := netip.ParseAddr(dataLif); err != nil {
				allErrs = append(allErrs, field.Invalid(dataLifPath, dataLif, "must be a valid IP address"))
			}
		}
	}

	return allErrs
}
