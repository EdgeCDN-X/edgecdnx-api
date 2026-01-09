package projects

import (
	"fmt"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type Config struct {
	Namespace string
}

type Module struct {
	Informer     cache.SharedIndexInformer
	cfg          Config
	client       *dynamic.DynamicClient
	informerChan chan struct{}
}

func New(cfg Config) *Module {
	return &Module{cfg: cfg}
}

func (m *Module) Shutdown() {
	logger.L().Info("Shutting down informer")
	close(m.informerChan)
}

func (m *Module) Init() error {
	logger.L().Info("Initializing module")

	client, err := app.GetK8SDynamicClient()

	if err != nil {
		return err
	}

	m.client = client

	fac := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 60*time.Minute, m.cfg.Namespace, nil)
	informer := fac.ForResource(schema.GroupVersionResource{
		Group:    infrastructurev1alpha1.SchemeGroupVersion.Group,
		Version:  infrastructurev1alpha1.SchemeGroupVersion.Version,
		Resource: "projects",
	}).Informer()

	informer.GetIndexer().AddIndexers(cache.Indexers{
		"projectName": func(obj any) ([]string, error) {

			fmt.Printf("Indexing project object: %+v\n", obj)

			raw, ok := obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("Object conversion failed")
			}

			project := &infrastructurev1alpha1.Project{}
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, project)
			if err != nil {
				return nil, err
			}

			return []string{project.Spec.Name}, nil
		},
	})

	stop := make(chan struct{})
	m.Informer = informer
	m.informerChan = stop

	go informer.Run(stop)

	if !cache.WaitForCacheSync(stop, informer.HasSynced) {
		logger.L().Error("Failed to sync informer cache")
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}
