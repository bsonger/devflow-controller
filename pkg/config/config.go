package config

import (
	"fmt"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"path/filepath"
)

func LoadKubeConfig() (*rest.Config, error) {
	// 本地 kubeconfig 优先
	if cfg, err := loadLocalKubeConfig(); err == nil {
		return cfg, nil
	}

	// Pod 内
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	return nil, fmt.Errorf("unable to load kubeconfig (local or in-cluster)")
}

func loadLocalKubeConfig() (*rest.Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	kubeconfig := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil, err
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
