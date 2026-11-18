package validation

import (
	"net/netip"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
)

// ValidateControllerConfiguration validates the ONTAP controller configuration.
func ValidateControllerConfiguration(controllerConfig *config.ControllerConfiguration) field.ErrorList {
	allErrs := field.ErrorList{}

	if controllerConfig == nil {
		return append(allErrs, field.Required(field.NewPath(""), "controller configuration must not be nil"))
	}

	clustersPath := field.NewPath("clusters")
	if len(controllerConfig.Clusters) == 0 {
		allErrs = append(allErrs, field.Required(clustersPath, "at least one cluster must be configured"))
	}

	seenNames := map[string]struct{}{}
	for i, cluster := range controllerConfig.Clusters {
		clusterPath := clustersPath.Index(i)

		if cluster.Name == "" {
			allErrs = append(allErrs, field.Required(clusterPath.Child("name"), "cluster name must not be empty"))
		} else if _, ok := seenNames[cluster.Name]; ok {
			allErrs = append(allErrs, field.Duplicate(clusterPath.Child("name"), cluster.Name))
		} else {
			seenNames[cluster.Name] = struct{}{}
		}

		ipPath := clusterPath.Child("ipaddress")
		switch {
		case cluster.IPAddress == "":
			allErrs = append(allErrs, field.Required(ipPath, "IP address must not be empty"))
		default:
			if _, err := netip.ParseAddr(cluster.IPAddress); err != nil {
				allErrs = append(allErrs, field.Invalid(ipPath, cluster.IPAddress, "must be a valid IP address"))
			}
		}

		if cluster.Username == "" {
			allErrs = append(allErrs, field.Required(clusterPath.Child("username"), "username must not be empty"))
		}
		if cluster.Password == "" {
			allErrs = append(allErrs, field.Required(clusterPath.Child("password"), "password must not be empty"))
		}
	}

	return allErrs
}
