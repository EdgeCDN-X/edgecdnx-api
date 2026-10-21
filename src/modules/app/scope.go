package app

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/cache"
)

const ProjectIndex = "spec.project.name"
const ResourceNameIndex = "metadata.name"

type Scope struct {
	Namespaced bool
	Namespace  string
}

func (s Scope) Validate() error {
	if !s.Namespaced {
		if problems := validation.IsDNS1123Label(s.Namespace); len(problems) != 0 {
			return fmt.Errorf("invalid shared namespace %q: %v", s.Namespace, problems)
		}
	}
	return nil
}

func (s Scope) ResourceNamespace(project string) string {
	if s.Namespaced {
		return project
	}
	return s.Namespace
}

func (s Scope) WatchNamespace() string {
	if s.Namespaced {
		return ""
	}
	return s.Namespace
}

func ProjectIndexFunc(object interface{}) ([]string, error) {
	resource, ok := object.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected an unstructured resource, got %T", object)
	}
	project, _, err := unstructured.NestedString(resource.Object, "spec", "project", "name")
	if err != nil {
		return nil, err
	}
	if project == "" {
		return nil, nil
	}
	return []string{resource.GetNamespace() + "/" + project}, nil
}

func ResourceNameIndexFunc(object interface{}) ([]string, error) {
	resource, ok := object.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected an unstructured resource, got %T", object)
	}
	return []string{resource.GetName()}, nil
}

func (s Scope) ProjectObjects(indexer cache.Indexer, project string) ([]interface{}, error) {
	return indexer.ByIndex(ProjectIndex, s.ResourceNamespace(project)+"/"+project)
}

func (s Scope) Owns(resource *unstructured.Unstructured, project string) bool {
	name, _, err := unstructured.NestedString(resource.Object, "spec", "project", "name")
	return err == nil && name != "" && name == project && resource.GetNamespace() == s.ResourceNamespace(project)
}
