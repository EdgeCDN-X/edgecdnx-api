package services

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type Config struct{}

type Module struct {
	cfg          Config
	client       *dynamic.DynamicClient
	infommerChan chan struct{}
}

func New(cfg Config) (*Module, error) {
	return &Module{cfg: cfg}, nil
}

func (m *Module) Shutdown() {
	log.Default().Println("edgecdnxservices: Shutting down informer")
	close(m.infommerChan)
}

func (m *Module) Init() error {

	fmt.Printf("edgecdnxservices: Initializing module\n")

	client, err := app.GetK8SDynamicClient()

	fmt.Printf("edgecdnxservices: Got dynamic client: %v, err: %v\n", client, err)

	if err != nil {
		return err
	}

	m.client = client

	fac := dynamicinformer.NewFilteredDynamicSharedInformerFactory(m.client, 5*time.Second, "argocd", nil)
	informer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "services",
	}).Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				fmt.Printf("edgecdnxservices: expected Service object, got %T", obj)
				return
			}

			temp, err := json.Marshal(s_raw.Object)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to marshal Service object: %v", err)
				return
			}
			service := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, service)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to unmarshal Service object: %v", err)
				return
			}

			fmt.Printf("edgecdnxservices: Added Service %s", service.Name)
		},
		UpdateFunc: func(oldObj, newObj any) {
			s_new_raw, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				fmt.Printf("edgecdnxservices: expected Service object, got %T", s_new_raw)
				return
			}

			temp, err := json.Marshal(s_new_raw.Object)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to marshal Service object: %v", err)
				return
			}
			newService := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, newService)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to unmarshal Service object: %v", err)
				return
			}

			fmt.Printf("edgecdnxservices: Updated Service %s", newService.Name)
		},
		DeleteFunc: func(obj any) {
			s_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				fmt.Printf("edgecdnxservices: expected Service object, got %T", obj)
				return
			}

			temp, err := json.Marshal(s_raw.Object)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to marshal Service object: %v", err)
				return
			}
			service := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, service)
			if err != nil {
				fmt.Printf("edgecdnxservices: failed to unmarshal Service object: %v", err)
				return
			}

			fmt.Printf("edgecdnxservices: Deleted Service %s", service.Name)
		},
	})

	// Start informer
	stopCh := make(chan struct{})
	m.infommerChan = stopCh
	go informer.Run(stopCh)

	fmt.Printf("edgecdnxservices: Watching Services in namespace %s", "argocd")

	return nil
}
