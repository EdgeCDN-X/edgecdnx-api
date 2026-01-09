package services

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var gvr = schema.GroupVersionResource{
	Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
	Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
	Resource: "services",
}

type Config struct{}

type Module struct {
	cfg          Config
	client       *dynamic.DynamicClient
	infommerChan chan struct{}
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) Shutdown() {

	logger.L().Info("Shutting down informer")
	close(m.infommerChan)
}

func (m *Module) Init() error {
	logger.L().Info("Initializing module")
	client, err := app.GetK8SDynamicClient()

	if err != nil {
		return err
	}

	m.client = client

	fac := dynamicinformer.NewDynamicSharedInformerFactory(m.client, 60*time.Minute)
	informer := fac.ForResource(gvr).Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Adding service: expected Service object, got different type")
				return
			}

			temp, err := json.Marshal(s_raw.Object)
			if err != nil {
				logger.L().Error("Failed to marshal Service object", zap.Error(err))
				return
			}
			service := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, service)
			if err != nil {
				logger.L().Error("Failed to unmarshal Service object", zap.Error(err))
				return
			}

			logger.L().Info("Added Service", zap.String("name", service.Name))
		},
		UpdateFunc: func(oldObj, newObj any) {
			s_new_raw, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Updating service: expected Service object, got different type")
				return
			}

			temp, err := json.Marshal(s_new_raw.Object)
			if err != nil {
				logger.L().Error("Failed to marshal Service object", zap.Error(err))
				return
			}
			newService := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, newService)
			if err != nil {
				logger.L().Error("Failed to unmarshal Service object", zap.Error(err))
				return
			}

			logger.L().Info("Updated Service", zap.String("name", newService.Name))
		},
		DeleteFunc: func(obj any) {
			s_raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.L().Error("Deleting service: expected Service object, got different type")
				return
			}

			temp, err := json.Marshal(s_raw.Object)
			if err != nil {
				logger.L().Error("Failed to marshal Service object", zap.Error(err))
				return
			}
			service := &infrastructurev1alpha1.Service{}
			err = json.Unmarshal(temp, service)
			if err != nil {
				logger.L().Error("Failed to unmarshal Service object", zap.Error(err))
				return
			}

			logger.L().Info("Deleted Service", zap.String("name", service.Name))
		},
	})

	// Start informer
	stop := make(chan struct{})
	m.infommerChan = stop
	go informer.Run(stop)

	logger.L().Info("Watching Services")

	if !cache.WaitForCacheSync(stop, informer.HasSynced) {
		logger.L().Error("Failed to sync informer cache")
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}
