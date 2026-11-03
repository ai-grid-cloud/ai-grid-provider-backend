package servers

import (
	"context"
	"fmt"
	"log/slog"

	connect "connectrpc.com/connect/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ServingOptionsKey is the ConfigMap data key that holds a serving options
// list, in both the defaults and the custom ConfigMap.
const ServingOptionsKey = "serving-options.yaml"

var configMapsResource = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// servingOptionsSource answers the current list. It's read on every
// ListServingOptions, Create and Update, so a changed ConfigMap applies
// without a restart.
type servingOptionsSource interface {
	load(ctx context.Context) (*ServingOptions, error)
}

// staticServingOptions is a fixed list, for tests.
type staticServingOptions struct{ options *ServingOptions }

func (s staticServingOptions) load(context.Context) (*ServingOptions, error) {
	return s.options, nil
}

// ConfigMapServingOptions reads the list from two ConfigMaps in one
// namespace on the hub: Defaults, owned by the console's Helm chart, and
// Custom, owned by the provider admin, which the chart never creates. Custom
// entries replace or add to the defaults by name (ServingOptions.merge). A
// missing custom ConfigMap means the defaults alone; one the backend can't
// read or parse is logged and ignored, so a mistake there never empties the
// wizard's list. A missing or invalid defaults ConfigMap is an error.
type ConfigMapServingOptions struct {
	Client    dynamic.Interface
	Namespace string
	Defaults  string
	// Custom is empty when there's no custom ConfigMap to read.
	Custom string
	Logger *slog.Logger
}

func (c *ConfigMapServingOptions) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}

func (c *ConfigMapServingOptions) load(ctx context.Context) (*ServingOptions, error) {
	data, err := c.read(ctx, c.Defaults)
	if apierrors.IsNotFound(err) {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"the serving options ConfigMap %s/%s doesn't exist", c.Namespace, c.Defaults)
	}
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "read the serving options: %v", err).WithCause(err)
	}
	defaults, err := parseServingOptions([]byte(data))
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal,
			"the serving options ConfigMap %s/%s is invalid: %v", c.Namespace, c.Defaults, err)
	}
	if c.Custom == "" {
		return defaults, nil
	}
	data, err = c.read(ctx, c.Custom)
	if apierrors.IsNotFound(err) {
		return defaults, nil
	}
	var custom *ServingOptions
	if err == nil {
		custom, err = parseServingOptions([]byte(data))
	}
	if err != nil {
		c.logger().Warn("custom serving options ignored", "namespace", c.Namespace, "configmap", c.Custom, "error", err)
		return defaults, nil
	}
	return defaults.merge(custom), nil
}

// read returns the ConfigMap's serving options text. A ConfigMap without the
// key is an error.
func (c *ConfigMapServingOptions) read(ctx context.Context, name string) (string, error) {
	cm, err := c.Client.Resource(configMapsResource).Namespace(c.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	data, _ := cm.Object["data"].(map[string]any)
	text, ok := data[ServingOptionsKey].(string)
	if !ok {
		return "", fmt.Errorf("it has no data key %q", ServingOptionsKey)
	}
	return text, nil
}
