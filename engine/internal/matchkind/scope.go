package matchkind

import (
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

// clusterScoped are the built-in kinds the API server serves without a
// namespace, by group and kind: those whose typed client in
// k8s.io/client-go/kubernetes takes no namespace (TestClusterScoped holds
// the list to it).
var clusterScoped = map[schema.GroupKind]bool{
	{Kind: "ComponentStatus"}:  true,
	{Kind: "Namespace"}:        true,
	{Kind: "Node"}:             true,
	{Kind: "PersistentVolume"}: true,
	{Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicy"}:          true,
	{Group: "admissionregistration.k8s.io", Kind: "MutatingAdmissionPolicyBinding"}:   true,
	{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration"}:     true,
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicy"}:        true,
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingAdmissionPolicyBinding"}: true,
	{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"}:   true,
	{Group: "certificates.k8s.io", Kind: "CertificateSigningRequest"}:                 true,
	{Group: "certificates.k8s.io", Kind: "ClusterTrustBundle"}:                        true,
	{Group: "flowcontrol.apiserver.k8s.io", Kind: "FlowSchema"}:                       true,
	{Group: "flowcontrol.apiserver.k8s.io", Kind: "PriorityLevelConfiguration"}:       true,
	{Group: "internal.apiserver.k8s.io", Kind: "StorageVersion"}:                      true,
	{Group: "networking.k8s.io", Kind: "IPAddress"}:                                   true,
	{Group: "networking.k8s.io", Kind: "IngressClass"}:                                true,
	{Group: "networking.k8s.io", Kind: "ServiceCIDR"}:                                 true,
	{Group: "node.k8s.io", Kind: "RuntimeClass"}:                                      true,
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"}:                         true,
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"}:                  true,
	{Group: "resource.k8s.io", Kind: "DeviceClass"}:                                   true,
	{Group: "resource.k8s.io", Kind: "DeviceTaintRule"}:                               true,
	{Group: "resource.k8s.io", Kind: "ResourcePoolStatusRequest"}:                     true,
	{Group: "resource.k8s.io", Kind: "ResourceSlice"}:                                 true,
	{Group: "scheduling.k8s.io", Kind: "PriorityClass"}:                               true,
	{Group: "storage.k8s.io", Kind: "CSIDriver"}:                                      true,
	{Group: "storage.k8s.io", Kind: "CSINode"}:                                        true,
	{Group: "storage.k8s.io", Kind: "StorageClass"}:                                   true,
	{Group: "storage.k8s.io", Kind: "VolumeAttachment"}:                               true,
	{Group: "storage.k8s.io", Kind: "VolumeAttributesClass"}:                          true,
	{Group: "storagemigration.k8s.io", Kind: "StorageVersionMigration"}:               true,
}

// Resource returns the resource the API server serves the built-in kind gvk
// as, such as apps/v1 deployments for apps/v1 Deployment, and whether it is
// namespaced; false for a kind that is not a built-in resource.
func Resource(gvk schema.GroupVersionKind) (gvr schema.GroupVersionResource, namespaced, ok bool) {
	t, known := typeOf(gvk)
	if !known || !isResource(gvk, t) {
		return schema.GroupVersionResource{}, false, false
	}
	return gvk.GroupVersion().WithResource(resourceName(gvk)), !clusterScoped[gvk.GroupKind()], true
}

// typeOf is the Go type of the built-in kind gvk.
func typeOf(gvk schema.GroupVersionKind) (reflect.Type, bool) {
	t, ok := scheme.Scheme.AllKnownTypes()[gvk]
	return t, ok
}
