package app

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type ResourceClient struct {
	dynamic.Interface
	indexers map[schema.GroupVersionResource]cache.Indexer
	cancel   context.CancelFunc
}

func NewResourceClient(client dynamic.Interface, scope Scope, resources ...schema.GroupVersionResource) (*ResourceClient, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, time.Hour, scope.WatchNamespace(), nil)
	result := &ResourceClient{Interface: client, indexers: make(map[schema.GroupVersionResource]cache.Indexer), cancel: cancel}
	syncs := make([]cache.InformerSynced, 0, len(resources))
	for _, resource := range resources {
		informer := factory.ForResource(resource).Informer()
		if err := informer.AddIndexers(cache.Indexers{ProjectIndex: ProjectIndexFunc, ResourceNameIndex: ResourceNameIndexFunc}); err != nil {
			cancel()
			return nil, err
		}
		result.indexers[resource] = informer.GetIndexer()
		syncs = append(syncs, informer.HasSynced)
	}
	factory.Start(ctx.Done())
	startup, stopStartup := context.WithTimeout(ctx, 30*time.Second)
	defer stopStartup()
	if !cache.WaitForCacheSync(startup.Done(), syncs...) {
		cancel()
		return nil, fmt.Errorf("failed to sync resource informers for %v", resources)
	}
	return result, nil
}

func CloseResourceClient(client dynamic.Interface) {
	if resourceClient, ok := client.(*ResourceClient); ok {
		resourceClient.cancel()
	}
}

func (r *ResourceClient) projectList(ctx context.Context, resource schema.GroupVersionResource, namespace, project string) (*unstructured.UnstructuredList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	indexer, ok := r.indexers[resource]
	if !ok {
		return nil, fmt.Errorf("resource %s has no informer", resource)
	}
	objects, err := indexer.ByIndex(ProjectIndex, namespace+"/"+project)
	if err != nil {
		return nil, err
	}
	return cachedList(objects)
}

func ListResources(ctx context.Context, client dynamic.Interface, resource schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resourceClient, ok := client.(*ResourceClient); ok {
		indexer, exists := resourceClient.indexers[resource]
		if !exists {
			return nil, fmt.Errorf("resource %s has no informer", resource)
		}
		if namespace == "" {
			return cachedList(indexer.List())
		}
		objects, err := indexer.ByIndex(cache.NamespaceIndex, namespace)
		if err != nil {
			return nil, err
		}
		return cachedList(objects)
	}
	return client.Resource(resource).Namespace(namespace).List(ctx, metav1.ListOptions{})
}

func cachedList(objects []interface{}) (*unstructured.UnstructuredList, error) {
	result := &unstructured.UnstructuredList{Items: make([]unstructured.Unstructured, 0, len(objects))}
	for _, object := range objects {
		resource, ok := object.(*unstructured.Unstructured)
		if !ok {
			return nil, fmt.Errorf("unexpected cached resource type %T", object)
		}
		result.Items = append(result.Items, *resource.DeepCopy())
	}
	return result, nil
}
