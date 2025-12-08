package app

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"

	"sync"

	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"

	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	dynamicClient     *dynamic.DynamicClient
	dynamicClientErr  error
	dynamicClientOnce sync.Once

	k8sClient     *kubernetes.Clientset
	k8sClientErr  error
	k8sClientOnce sync.Once
)

func GetK8SDynamicClient() (*dynamic.DynamicClient, error) {
	dynamicClientOnce.Do(func() {
		runtimescheme := runtime.NewScheme()
		scheme.AddToScheme(runtimescheme)
		infrastructurev1alpha1.AddToScheme(runtimescheme)
		kubeconfig := ctrl.GetConfigOrDie()
		dynamicClient, dynamicClientErr = dynamic.NewForConfig(kubeconfig)
	})
	return dynamicClient, dynamicClientErr
}

func GetK8SClient() (*kubernetes.Clientset, error) {
	k8sClientOnce.Do(func() {
		kubeconfig := ctrl.GetConfigOrDie()
		k8sClient, k8sClientErr = kubernetes.NewForConfig(kubeconfig)
	})
	return k8sClient, k8sClientErr
}
