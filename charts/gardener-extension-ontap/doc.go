//go:generate sh -c "bash $GARDENER_HACK_DIR/generate-controller-registration.sh ontap . v0.1.0 ../../example/controller-registration.yaml Extension:ontap"

// Package chart enables go:generate support for generating the correct controller registration.
package chart
