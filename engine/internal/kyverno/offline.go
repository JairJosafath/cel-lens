package kyverno

import (
	"errors"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/kyverno/sdk/extensions/cel/libs/http"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// errOffline is what a function that needs the cluster, a registry or the
// network returns: expressions are compiled here, never run.
var errOffline = errors.New("not available while checking a policy")

// offline stands in for the cluster, registries and the network in the function
// libraries, which want a context to be built. Compiling needs only the
// declarations of their functions, so every call fails. The zero value is ready
// to use, and it is safe for concurrent use.
type offline struct{}

func (offline) ListResources(string, string, string, map[string]string) (*unstructured.UnstructuredList, error) {
	return nil, errOffline
}

func (offline) GetResource(string, string, string, string) (*unstructured.Unstructured, error) {
	return nil, errOffline
}

func (offline) PostResource(string, string, string, map[string]any) (*unstructured.Unstructured, error) {
	return nil, errOffline
}

func (offline) ToGVR(string, string) (*schema.GroupVersionResource, error) {
	return nil, errOffline
}

func (offline) GetGlobalReference(string, string) (any, error) { return nil, errOffline }

func (offline) GetImageData(string, []remote.Option) (map[string]any, error) {
	return nil, errOffline
}

func (offline) Get(string, map[string]string) (any, error)       { return nil, errOffline }
func (offline) Post(string, any, map[string]string) (any, error) { return nil, errOffline }

func (offline) Client(string) (http.ContextInterface, error) { return nil, errOffline }
