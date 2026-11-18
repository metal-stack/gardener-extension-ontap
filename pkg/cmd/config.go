package cmd

import (
	"errors"
	"os"

	healthcheckconfig "github.com/gardener/gardener/extensions/pkg/apis/config/v1alpha1"
	configapi "github.com/metal-stack/gardener-extension-ontap/pkg/apis/config"
	"github.com/metal-stack/gardener-extension-ontap/pkg/apis/config/v1alpha1"
	configvalidation "github.com/metal-stack/gardener-extension-ontap/pkg/apis/config/validation"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

var (
	scheme  *runtime.Scheme
	decoder runtime.Decoder
)

func init() {
	scheme = runtime.NewScheme()
	utilruntime.Must(configapi.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	decoder = serializer.NewCodecFactory(scheme).UniversalDecoder()
}

// ControllerConfigurationOptions holds options for the ONTAP controller configuration.
type ControllerConfigurationOptions struct {
	ConfigLocation string
	config         *ControllerConfigurationConfig
}

// AddFlags implements Flagger.AddFlags.
func (o *ControllerConfigurationOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.ConfigLocation, "config", "", "Path to the ONTAP controller configuration")
}

// Complete implements Completer.Complete.
func (o *ControllerConfigurationOptions) Complete() error {
	if o.ConfigLocation == "" {
		return errors.New("config location is not set")
	}
	data, err := os.ReadFile(o.ConfigLocation)
	if err != nil {
		return err
	}

	config := configapi.ControllerConfiguration{}
	_, _, err = decoder.Decode(data, nil, &config)
	if err != nil {
		return err
	}

	if errs := configvalidation.ValidateControllerConfiguration(&config); len(errs) > 0 {
		return errs.ToAggregate()
	}

	o.config = &ControllerConfigurationConfig{
		config: config,
	}

	return nil
}

// Completed returns the decoded controller configuration. Only call this if Complete was successful.
func (o *ControllerConfigurationOptions) Completed() *ControllerConfigurationConfig {
	return o.config
}

// ControllerConfigurationConfig contains the controller configuration.
type ControllerConfigurationConfig struct {
	config configapi.ControllerConfiguration
}

// Apply applies the configuration to the passed ControllerConfiguration instance.
func (c *ControllerConfigurationConfig) Apply(config *configapi.ControllerConfiguration) {
	*config = c.config
}

// ApplyHealthCheckConfig applies the HealthCheckConfig.
func (c *ControllerConfigurationConfig) ApplyHealthCheckConfig(config *healthcheckconfig.HealthCheckConfig) {
	if c.config.HealthCheckConfig != nil {
		*config = *c.config.HealthCheckConfig
	}
}
