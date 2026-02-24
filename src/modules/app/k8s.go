package app

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sync"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"

	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	dynamicClient         *dynamic.DynamicClient
	dynamicClientErr      error
	dynamicClientOnce     sync.Once
	dynamicBaseRestConfig *rest.Config

	k8sClient      *kubernetes.Clientset
	k8sClientErr   error
	k8sClientOnce  sync.Once
	baseRestConfig *rest.Config
)

func GetK8SDynamicClient() (*dynamic.DynamicClient, *rest.Config, error) {
	dynamicClientOnce.Do(func() {
		runtimescheme := runtime.NewScheme()
		scheme.AddToScheme(runtimescheme)
		infrastructurev1alpha1.AddToScheme(runtimescheme)
		kubeconfig := ctrl.GetConfigOrDie()
		dynamicClient, dynamicClientErr = dynamic.NewForConfig(kubeconfig)
		dynamicBaseRestConfig = kubeconfig
	})
	return dynamicClient, dynamicBaseRestConfig, dynamicClientErr
}

func GetK8SClient() (*kubernetes.Clientset, *rest.Config, error) {
	k8sClientOnce.Do(func() {
		kubeconfig := ctrl.GetConfigOrDie()
		k8sClient, k8sClientErr = kubernetes.NewForConfig(kubeconfig)
		baseRestConfig = kubeconfig
	})
	return k8sClient, baseRestConfig, k8sClientErr
}

// WarningCollector stores warnings in a slice
type WarningCollector struct {
	Warnings []string
}

// Implement rest.WarningHandler
func (w *WarningCollector) HandleWarningHeader(code int, agent, text string) {
	w.Warnings = append(w.Warnings, text)
}
