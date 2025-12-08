package projects

import (
	"fmt"
	"time"

	"github.com/EdgeCDN-X/edgecdnx-api/src/internal/logger"
	"github.com/EdgeCDN-X/edgecdnx-api/src/modules/app"
	"go.uber.org/zap"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Config struct {
	ProjectLabelNamespaceSelector string
	ProjectNameLabel              string
}

type Module struct {
	Informer     cache.SharedIndexInformer
	cfg          Config
	client       *kubernetes.Clientset
	informerChan chan struct{}
}

func New(cfg Config) (*Module, error) {
	return &Module{cfg: cfg}, nil
}

func (m *Module) Shutdown() {
	logger.L().Info("Shutting down informer")
	close(m.informerChan)
}

func (m *Module) Init() error {
	logger.L().Info("Initializing module")

	client, err := app.GetK8SClient()

	if err != nil {
		return err
	}

	m.client = client

	fac := informers.NewFilteredSharedInformerFactory(client, 60*time.Minute, "", func(opts *metav1.ListOptions) {
		opts.LabelSelector = m.cfg.ProjectLabelNamespaceSelector
	})
	informer := fac.Core().V1().Namespaces().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			ns, ok := obj.(*v1.Namespace)
			if !ok {
				logger.L().Error("Adding namespace: expected Namespace object, got different type")
				return
			}

			logger.L().Info("Added namespace", zap.String("name", ns.Name))
		},
		UpdateFunc: func(oldObj, newObj any) {
			oldNs, ok := oldObj.(*v1.Namespace)
			newNs, ok2 := newObj.(*v1.Namespace)
			if !ok || !ok2 {
				logger.L().Error("Updating namespace: expected Namespace object, got different type")
				return
			}

			logger.L().Info("Updated namespace", zap.String("name", newNs.Name), zap.String("oldName", oldNs.Name))
		},
		DeleteFunc: func(obj any) {
			ns, ok := obj.(*v1.Namespace)
			if !ok {
				logger.L().Error("Deleting namespace: expected Namespace object, got different type")
				return
			}

			logger.L().Info("Deleted namespace", zap.String("name", ns.Name))
		},
	})

	informer.GetIndexer().AddIndexers(cache.Indexers{
		"projectName": func(obj any) ([]string, error) {
			ns, ok := obj.(*v1.Namespace)
			if !ok {
				return nil, fmt.Errorf("expected Namespace object, got %T", obj)
			}

			if name, exists := ns.Labels[m.cfg.ProjectNameLabel]; exists {
				return []string{name}, nil
			}
			return []string{}, nil
		},
	})

	stop := make(chan struct{})
	m.Informer = informer
	m.informerChan = stop

	go informer.Run(stop)

	logger.L().Info("Watching namespaces with label selector", zap.String("selector", m.cfg.ProjectLabelNamespaceSelector))

	if !cache.WaitForCacheSync(stop, informer.HasSynced) {
		logger.L().Error("Failed to sync informer cache")
		return fmt.Errorf("failed to sync informer cache")
	}

	return nil
}
