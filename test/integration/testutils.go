package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func kubeConfig() (*rest.Config, error) {
	cfg, err := k()
	if err != nil {
		return nil, err
	}
	cfg.QPS = 50
	cfg.Burst = 100
	return cfg, nil
}

func k() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}

func dynClient(t *testing.T) dynamic.Interface {
	cfg, err := kubeConfig()
	require.NoError(t, err)
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	return dc
}
