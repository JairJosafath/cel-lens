package examples

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	auditinternal "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/util/compatibility"
	"k8s.io/apiserver/pkg/warning"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/component-base/featuregate"
	sigsyaml "sigs.k8s.io/yaml"
)

// oracleCase is a workspace for TestPolicyRunMatchesAPIServer: a policy and
// one binding in the policy's file, written with every field the API server
// defaults, and other files.
type oracleCase struct {
	name   string
	policy string
	files  [][2]string
}

// apiServer is the API server's ValidatingAdmissionPolicy plugin, as it
// runs in an API server: its informers over a fake clientset, for the
// policies, bindings, namespaces and ConfigMap params, and a fake dynamic
// client for the params of a custom resource.
type apiServer struct {
	plugin *validating.Plugin
	oi     admission.ObjectInterfaces
}

// allowAll is the authorizer the plugin is given; no case reads it.
var allowAll = authorizer.AuthorizerFunc(func(context.Context, authorizer.Attributes) (authorizer.Decision, string, error) {
	return authorizer.DecisionAllow, "", nil
})

// newAPIServer starts the plugin with the documents of texts.
func newAPIServer(t *testing.T, texts ...string) apiServer {
	t.Helper()
	var typed, dynamic []runtime.Object
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), meta.RESTScopeNamespace)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Namespace"), meta.RESTScopeRoot)
	listKinds := map[schema.GroupVersionResource]string{}
	dynScheme := runtime.NewScheme()
	for _, text := range texts {
		for doc := range strings.SplitSeq(text, "\n---\n") {
			var u unstructured.Unstructured
			if err := sigsyaml.Unmarshal([]byte(doc), &u.Object); err != nil || u.Object == nil {
				continue
			}
			gvk := u.GroupVersionKind()
			switch {
			case gvk.Group == "apiextensions.k8s.io":
			case gvk.Group == "admissionregistration.k8s.io" || gvk.Kind == "ConfigMap" || gvk.Kind == "Namespace":
				obj, err := scheme.Scheme.New(gvk)
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj); err != nil {
					t.Fatal(err)
				}
				if ns, ok := obj.(*corev1.Namespace); ok {
					withNameLabel(ns)
				}
				if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Namespace == "" {
					cm.Namespace = "default"
				}
				typed = append(typed, obj)
			case gvk.Kind == "WidgetLimit":
				if u.GetNamespace() == "" {
					u.SetNamespace("default")
				}
				mapper.Add(gvk, meta.RESTScopeNamespace)
				gvr := gvk.GroupVersion().WithResource("widgetlimits")
				listKinds[gvr] = "WidgetLimitList"
				dynScheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
				dynScheme.AddKnownTypeWithName(gvk.GroupVersion().WithKind("WidgetLimitList"), &unstructured.UnstructuredList{})
				dynamic = append(dynamic, &u)
			}
		}
	}
	// A cluster has the namespace default.
	def := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: metav1.NamespaceDefault}}
	withNameLabel(def)
	typed = append(typed, def)
	client := fake.NewSimpleClientset(typed...)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(dynScheme, listKinds, dynamic...)
	factory := informers.NewSharedInformerFactory(client, 0)
	plugin, err := validating.NewPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	initializer.New(client, dyn, factory, allowAll, featuregate.NewFeatureGate(), compatibility.DefaultBuildEffectiveVersion(), stop, mapper).Initialize(plugin)
	if err := plugin.ValidateInitialization(); err != nil {
		t.Fatal(err)
	}
	factory.Start(stop)
	return apiServer{plugin: plugin, oi: admission.NewObjectInterfacesFromScheme(runtime.NewScheme())}
}

// auditAttributes are the attributes of a request, keeping its audit
// annotations.
type auditAttributes struct {
	admission.Attributes
	mu          sync.Mutex
	annotations map[string]string
}

func (a *auditAttributes) AddAnnotation(key, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.annotations == nil {
		a.annotations = map[string]string{}
	}
	a.annotations[key] = value
	return nil
}

func (a *auditAttributes) AddAnnotationWithLevel(key, value string, _ auditinternal.Level) error {
	return a.AddAnnotation(key, value)
}

// admit is the plugin's answer to the request for adm's example: the
// error, the warnings and the audit annotations.
func (s apiServer) admit(t *testing.T, adm Admission, gvr schema.GroupVersionResource, namespaced bool) (string, []string, map[string]string) {
	t.Helper()
	o := adm.Example
	obj := &unstructured.Unstructured{Object: runtime.DeepCopyJSON(o.Object)}
	ns := ""
	if namespaced {
		ns = obj.GetNamespace()
	}
	op, options := admission.Create, runtime.Object(&metav1.CreateOptions{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "CreateOptions"}})
	var old runtime.Object
	if adm.Old != nil {
		old = &unstructured.Unstructured{Object: runtime.DeepCopyJSON(adm.Old.Object)}
		op, options = admission.Update, &metav1.UpdateOptions{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "UpdateOptions"}}
	}
	attrs := &auditAttributes{Attributes: admission.NewAttributesRecord(obj, old, obj.GroupVersionKind(), ns, obj.GetName(), gvr, "", op, options, false, &user.DefaultInfo{})}
	var w recorder
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	err := s.plugin.Validate(warning.WithWarningRecorder(ctx, &w), attrs, s.oi)
	denied := ""
	if err != nil {
		denied = err.Error()
	}
	return denied, w.texts, attrs.annotations
}

// TestPolicyRunMatchesAPIServer holds what Run answers for each example
// under each binding to what the API server's ValidatingAdmissionPolicy
// plugin answers, running as in an API server with the same policy,
// binding, params and namespaces in its informers: the error it refuses the
// example with, its warnings and its audit annotations, exactly. It also
// holds each result to that answer: the example is refused exactly when a
// result refuses it or the binding does, and each warned or failed result
// under Warn is a warning.
//
// It does not run in parallel: the plugin compiles its policies once for
// the Kubernetes version the process is set to, which other tests change.
func TestPolicyRunMatchesAPIServer(t *testing.T) { //nolint:paralleltest // see the doc comment
	deploy := func(name, labels string, replicas int) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + ", labels: {" + labels + "}}\nspec: {replicas: " + strconv.Itoa(replicas) +
			", selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n"
	}
	inNamespace := func(name, ns string, replicas int) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + ", namespace: " + ns + "}\nspec: {replicas: " + strconv.Itoa(replicas) +
			", selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n"
	}
	header := func(name, extra string) string {
		return "apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata: {name: " + name + "}\nspec:\n  failurePolicy: Fail\n" + extra
	}
	constraints := func(selectors string) string {
		return `  matchConstraints:
    matchPolicy: Equivalent
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments], scope: "*"}
` + selectors
	}
	everything := "    namespaceSelector: {}\n    objectSelector: {}\n"
	binding := func(name, policy, actions, rest string) string {
		return "---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: " + name + "}\nspec:\n  policyName: " + policy + "\n  validationActions: [" + actions + "]\n" + rest
	}
	limits := `apiVersion: v1
kind: ConfigMap
metadata: {name: small, labels: {limits: "yes"}}
data: {max: "3"}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: big, labels: {limits: "yes"}}
data: {max: "10"}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: other}
data: {max: "1"}
`
	deploys := deploy("one", "", 1) + "---\n" + deploy("five", "tier: web", 5) + "---\n" + deploy("test-fifty", "", 50) + "---\n" + deploy("zero", "skip: \"1\"", 0)
	replicasRules := `  validations:
    - expression: "object.spec.replicas <= int(params.data.max)"
      messageExpression: "'at most ' + params.data.max + ' replicas'"
    - expression: "object.spec.replicas > 0"
      message: no zero replicas
      reason: Forbidden
`
	paramKind := "  paramKind: {apiVersion: v1, kind: ConfigMap}\n"
	for _, tc := range []oracleCase{{
		name: "Warn, with a paramRef.selector matching two params",
		policy: header("replicas", paramKind+constraints(everything)+replicasRules) +
			binding("warn", "replicas", "Warn", "  paramRef: {selector: {matchLabels: {limits: \"yes\"}}, namespace: default, parameterNotFoundAction: Deny}\n"),
		files: [][2]string{{"params.yaml", limits}, {"deploy.yaml", deploys}},
	}, {
		name: "Deny, with a paramRef.name, and an objectSelector",
		policy: header("replicas", paramKind+constraints("    namespaceSelector: {}\n    objectSelector: {matchExpressions: [{key: skip, operator: DoesNotExist}]}\n")+replicasRules) +
			binding("deny", "replicas", "Deny", "  paramRef: {name: small, parameterNotFoundAction: Deny}\n"),
		files: [][2]string{{"params.yaml", limits}, {"deploy.yaml", deploys}},
	}, {
		name: "Deny and Audit, a match condition excluding an example, variables and audit annotations",
		policy: header("named", constraints(everything)+`  matchConditions:
    - name: not-test
      expression: "!object.metadata.name.startsWith('test')"
  variables:
    - name: replicas
      expression: "object.spec.replicas"
  validations:
    - expression: "variables.replicas < 3"
      message: "fewer than 3"
  auditAnnotations:
    - key: replicas
      valueExpression: "string(variables.replicas)"
    - key: none
      valueExpression: "null"
`) + binding("deny-audit", "named", "Deny, Audit", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "Audit alone",
		policy: header("audit", constraints(everything)+"  validations:\n    - expression: \"object.spec.replicas < 3\"\n") +
			binding("audit", "audit", "Audit", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "a runtime error, under failurePolicy Fail, in a validation and in an audit annotation",
		policy: header("fails", constraints(everything)+"  validations:\n    - expression: \"object.spec.replicas / (object.spec.replicas - 5) >= 0\"\n    - expression: \"object.spec.replicas < 3\"\n"+
			"  auditAnnotations:\n    - key: ratio\n      valueExpression: \"string(10 / (object.spec.replicas - 1))\"\n") +
			binding("deny", "fails", "Deny", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "a runtime error, under failurePolicy Ignore and Warn",
		policy: strings.Replace(header("ignores", constraints(everything)+"  validations:\n    - expression: \"object.spec.replicas / (object.spec.replicas - 5) >= 0\"\n"), "Fail", "Ignore", 1) +
			binding("warn", "ignores", "Warn", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "a match condition that fails, under failurePolicy Ignore",
		policy: strings.Replace(header("ratio", constraints(everything)+"  matchConditions:\n    - {name: ratio, expression: \"10 / (object.spec.replicas - 1) > 0\"}\n  validations:\n    - expression: \"object.spec.replicas < 3\"\n"), "Fail", "Ignore", 1) +
			binding("deny", "ratio", "Deny", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "a match condition that fails, under failurePolicy Fail",
		policy: header("ratio", constraints(everything)+"  matchConditions:\n    - {name: ratio, expression: \"10 / (object.spec.replicas - 1) > 0\"}\n  validations:\n    - expression: \"object.spec.replicas < 3\"\n") +
			binding("deny", "ratio", "Deny", ""),
		files: [][2]string{{"deploy.yaml", deploys}},
	}, {
		name: "params not found, Deny",
		policy: header("replicas", paramKind+constraints(everything)+replicasRules) +
			binding("missing", "replicas", "Deny", "  paramRef: {name: absent, parameterNotFoundAction: Deny}\n"),
		files: [][2]string{{"params.yaml", limits}, {"deploy.yaml", deploys}},
	}, {
		name: "params not found, Allow",
		policy: header("replicas", paramKind+constraints(everything)+replicasRules) +
			binding("missing", "replicas", "Deny", "  paramRef: {name: absent, parameterNotFoundAction: Allow}\n"),
		files: [][2]string{{"params.yaml", limits}, {"deploy.yaml", deploys}},
	}, {
		name: "a namespaceSelector, namespaceObject, and a binding's matchResources",
		policy: header("prod", constraints("    namespaceSelector: {matchLabels: {env: prod}}\n    objectSelector: {}\n")+
			"  validations:\n    - expression: \"object.spec.replicas >= 2\"\n      messageExpression: \"'in ' + namespaceObject.metadata.name + ', at least 2'\"\n") +
			binding("narrow", "prod", "Deny", `  matchResources:
    matchPolicy: Equivalent
    namespaceSelector: {}
    objectSelector: {}
    excludeResourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE], resources: [deployments], resourceNames: [excused], scope: "*"}
`),
		files: [][2]string{
			{"ns.yaml", "apiVersion: v1\nkind: Namespace\nmetadata: {name: shop, labels: {env: prod}}\n---\napiVersion: v1\nkind: Namespace\nmetadata: {name: lab, labels: {env: dev}}\n"},
			{"deploy.yaml", inNamespace("a", "shop", 1) + "---\n" + inNamespace("excused", "shop", 1) + "---\n" + inNamespace("b", "lab", 1) + "---\n" + inNamespace("c", "shop", 3)},
		},
	}, {
		name: "an update, with oldObject",
		policy: header("grow", constraints(everything)+"  validations:\n    - expression: \"request.operation == 'CREATE' || object.spec.replicas >= oldObject.spec.replicas\"\n      message: never fewer\n") +
			binding("deny", "grow", "Deny", ""),
		files: [][2]string{{"deploy.yaml", deploy("w", "", 3) + "---\n" + deploy("w", "", 5) + "---\n" + deploy("w", "", 2) + "---\n" + deploy("w", "", 4)}},
	}, {
		name: "a custom resource, with custom resource params",
		policy: header("widgets", `  paramKind: {apiVersion: example.com/v1, kind: WidgetLimit}
  matchConstraints:
    matchPolicy: Equivalent
    namespaceSelector: {}
    objectSelector: {}
    resourceRules:
      - {apiGroups: [example.com], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [widgets], scope: "*"}
  validations:
    - expression: "object.spec.size <= params.spec.max"
      messageExpression: "'size at most ' + string(params.spec.max)"
`) + binding("deny", "widgets", "Deny", "  paramRef: {name: limit, parameterNotFoundAction: Deny}\n"),
		files: [][2]string{
			{"crd.yaml", specCRD("{type: object, properties: {size: {type: integer}}}") + "---\n" + limitCRD},
			{"limit.yaml", "apiVersion: example.com/v1\nkind: WidgetLimit\nmetadata: {name: limit}\nspec: {max: 5}\n"},
			{"widgets.yaml", specWidget("small", "{size: 2}") + "---\n" + specWidget("big", "{size: 20}")},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ws := readWorkspace(t, tc.policy, tc.files...)
			run := ws.run(t)
			if len(run.Admissions) == 0 {
				t.Fatal("Run admitted no example")
			}
			texts := []string{tc.policy}
			for _, f := range tc.files {
				texts = append(texts, f[1])
			}
			server := newAPIServer(t, texts...)
			for _, adm := range run.Admissions {
				o := adm.Example
				gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
				if o.CRD != nil {
					gvr = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
				}
				denied, warnings, annotations := server.admit(t, adm, gvr, true)
				at := strings.TrimPrefix(o.File, "/w/") + ":" + strconv.Itoa(o.Line)
				if adm.Denied != denied {
					t.Errorf("%s: Run denied %q, the API server %q", at, adm.Denied, denied)
				}
				// The API server lists the params of a paramRef.selector in no
				// fixed order, and warns in that order.
				if !slices.Equal(slices.Sorted(slices.Values(adm.Warnings)), slices.Sorted(slices.Values(warnings))) {
					t.Errorf("%s: Run warned %q, the API server %q", at, adm.Warnings, warnings)
				}
				if !maps.Equal(adm.Annotations, annotations) {
					t.Errorf("%s: Run annotated %v, the API server %v", at, adm.Annotations, annotations)
				}
				checkResultsExplain(t, at, run, adm)
			}
		})
	}
}

// checkResultsExplain checks the results of the example of adm against
// adm: the example is refused exactly when a result refuses it or a binding
// refuses it without running the policy, and the warnings are the warned
// results and the errors that do not refuse under Warn.
func checkResultsExplain(t *testing.T, at string, run PolicyRun, adm Admission) {
	t.Helper()
	refuses, warned := false, 0
	for _, r := range run.Results {
		if r.Example != adm.Example || r.Binding != adm.Binding {
			continue
		}
		refuses = refuses || r.Refuses || r.Cause == CauseBindingRefused
		failClosed := *r.Rule.Policy.spec.Spec.FailurePolicy == admissionregistrationv1.Fail
		if r.Verdict == PolicyWarned || r.Verdict == PolicyError && r.Rule.Field == FieldValidation && failClosed && adm.Binding != nil && slices.Contains(adm.Binding.Actions(), "Warn") {
			warned++
		}
	}
	if refuses != (adm.Denied != "") {
		t.Errorf("%s: the results refuse the example: %v, the API server denied %q", at, refuses, adm.Denied)
	}
	if warned != len(adm.Warnings) {
		t.Errorf("%s: %d results warn, the API server gave %d warnings %q", at, warned, len(adm.Warnings), adm.Warnings)
	}
}

// limitCRD is the CustomResourceDefinition of the param kind WidgetLimit.
const limitCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgetlimits.example.com
spec:
  group: example.com
  names: {plural: widgetlimits, singular: widgetlimit, kind: WidgetLimit, listKind: WidgetLimitList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec: {type: object, properties: {max: {type: integer}}}
`
