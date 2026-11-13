package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests of the examples command run the built program on a fixture
// repository: a CRD with a rule, a ValidatingAdmissionPolicy with its binding,
// and the examples of both, the way a project lays them out.

const examplesCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                size: {type: integer}
              x-kubernetes-validations:
                - rule: %s
                  message: at most 10
`

const examplesPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: replicas}
spec:
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
  validations:
    - expression: "object.spec.replicas <= 3"
      message: at most 3 replicas
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata: {name: replicas}
spec:
  policyName: replicas
  validationActions: [Deny]
`

// widgetExample is a Widget of the size, which is a YAML value.
func widgetExample(size string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec: {size: " + size + "}\n"
}

// deploymentExample is a Deployment with that many replicas.
func deploymentExample(replicas string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec: {replicas: " + replicas + ", selector: {matchLabels: {a: b}}, template: {metadata: {labels: {a: b}}, spec: {containers: [{name: c, image: i}]}}}\n"
}

// examplesRepo writes a repository whose rules each have an example they let
// through and one they refuse, which expects it (invalid/), and more files, by
// path, and returns its folder.
func examplesRepo(t *testing.T, rule string, more map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"config/crd/widget.yaml":                  strings.Replace(examplesCRD, "%s", rule, 1),
		"config/policy/replicas.yaml":             examplesPolicy,
		"config/samples/widget.yaml":              widgetExample("3"),
		"config/samples/invalid/big-widget.yaml":  widgetExample("11"),
		"config/samples/deployment.yaml":          deploymentExample("2"),
		"config/samples/invalid/big-deploys.yaml": deploymentExample("5"),
	}
	for path, text := range more {
		files[path] = text
	}
	for path, text := range files {
		path = filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runExamplesBinary runs the built program's examples command in dir and
// returns its exit code and what it wrote.
func runExamplesBinary(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), buildBinary(t), append([]string{"examples"}, args...)...) //nolint:gosec // bin is the binary make test built
	cmd.Dir = dir
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	if exit := (&exec.ExitError{}); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errs.String()
}

// TestBinaryExamples runs the built program on a fixture repository and
// checks each exit status and what it prints: 0 when every example is as it
// expects, 1 on a wrong verdict, which --fail-on none and a verdict that is an
// error do not make it, and 2 on a usage or read error (#24).
func TestBinaryExamples(t *testing.T) {
	t.Parallel()
	const bigWidget = "config/samples/huge.yaml"
	good := examplesRepo(t, "self.size <= 10", map[string]string{"notes.txt": "not YAML"})
	wrong := examplesRepo(t, "self.size <= 10", map[string]string{
		bigWidget:                         widgetExample("20"),
		"config/samples/too-many.yaml":    deploymentExample("8"),
		"config/samples/string-size.yaml": widgetExample(`"three"`),
	})
	// A rule that stops with an error refuses the example, as the API server
	// answers the request with it.
	erroring := examplesRepo(t, "100 / self.size > 0 && self.size <= 10", map[string]string{"config/samples/zero.yaml": widgetExample("0")})
	expectedError := examplesRepo(t, "100 / self.size > 5", map[string]string{"config/samples/invalid/big-widget.yaml": widgetExample("0")})
	policyError := examplesRepo(t, "self.size <= 10", map[string]string{
		"config/policy/replicas.yaml":       strings.Replace(examplesPolicy, "object.spec.replicas <= 3", "100 / object.spec.replicas > 20", 1),
		"config/samples/zero-replicas.yaml": deploymentExample("0"),
	})
	limited := examplesRepo(t, "self.size <= 10", map[string]string{"config/samples/big.yaml": strings.Repeat("# filler\n", 1<<17+1)})
	tests := []struct {
		name       string
		dir        string
		args       []string
		wantCode   int
		wantStdout []string // each a part of the output
		wantExact  string   // all of the output, when not ""
		wantStderr string   // a part of it, "" for none
	}{
		{
			name: "every example is as expected", dir: good, args: []string{"./config"}, wantCode: exitOK,
			wantExact: "2 rules, 4 examples: 0 wrong\n",
		},
		{
			name: "no paths run the current folder", dir: good, wantCode: exitOK,
			wantExact: "2 rules, 4 examples: 0 wrong\n",
		},
		{
			name: "verbose lists every result", dir: good, args: []string{"--verbose", "config"}, wantCode: exitOK,
			wantStdout: []string{
				"config/crd/widget.yaml:21: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)\n",
				"  ok    config/samples/invalid/big-widget.yaml:1: refused: spec: Invalid value: at most 10\n",
				"  ok    config/samples/widget.yaml:1: allowed\n",
				"config/policy/replicas.yaml:9: object.spec.replicas <= 3  (ValidatingAdmissionPolicy replicas, validation)\n",
				"  ok    config/samples/deployment.yaml:1: allowed under binding \"replicas\"\n",
				"2 rules, 4 examples: 0 wrong\n",
			},
		},
		{
			name: "a wrong verdict fails", dir: wrong, args: []string{"config"}, wantCode: exitFail,
			wantStdout: []string{
				"config/samples/string-size.yaml:4:8: error: spec.size: Invalid value: \"string\"",
				`wrong config/samples/huge.yaml:1: refused, expected to be allowed: spec: Invalid value: at most 10`,
				`wrong config/samples/too-many.yaml:1: refused under binding "replicas", expected to be allowed: at most 3 replicas`,
				"2 rules, 7 examples: 3 wrong, 1 not run\n",
			},
		},
		{
			name: "wrong verdicts come first, marked", dir: wrong, args: []string{"config"}, wantCode: exitFail,
			wantStdout: []string{"  wrong config/samples/huge.yaml:1: refused, expected to be allowed"},
		},
		{
			name: "fail on none", dir: wrong, args: []string{"--fail-on", "none", "config"}, wantCode: exitOK,
			wantStdout: []string{"huge.yaml:1: refused", "2 rules, 7 examples: 3 wrong, 1 not run\n"},
		},
		{
			name: "flags after the path", dir: wrong, args: []string{"config", "--fail-on=none", "--kube-version", "1.34"}, wantCode: exitOK,
			wantStdout: []string{"3 wrong"},
		},
		{
			name: "the other name of the Kubernetes version", dir: wrong, args: []string{"--kubernetes-version=1.34", "config"}, wantCode: exitFail,
			wantStdout: []string{"3 wrong"},
		},
		{
			name: "an error that refuses an example expected to be allowed fails", dir: erroring, args: []string{"."}, wantCode: exitFail,
			wantStdout: []string{
				"  wrong config/samples/zero.yaml:1: error, expected to be allowed: spec: Invalid value:",
				"division by zero",
				"2 rules, 5 examples: 1 wrong\n",
			},
		},
		{
			name: "a policy's error under Fail and Deny fails", dir: policyError, args: []string{"."}, wantCode: exitFail,
			wantStdout: []string{
				`  wrong config/samples/zero-replicas.yaml:1: error under binding "replicas", expected to be allowed:`,
				"division by zero",
			},
		},
		{
			name: "an error that the example expects passes by default", dir: expectedError, args: []string{"."}, wantCode: exitOK,
			wantStdout: []string{
				"  error config/samples/invalid/big-widget.yaml:1: error: spec: Invalid value:",
				"0 wrong, 1 error",
			},
		},
		{
			name: "an error that the example expects fails on error", dir: expectedError, args: []string{"--fail-on", "error", "."}, wantCode: exitFail,
			wantStdout: []string{"0 wrong, 1 error"},
		},
		{
			name: "an error that the example does not expect fails on error too", dir: erroring, args: []string{"--fail-on=error", "."}, wantCode: exitFail,
			wantStdout: []string{"1 wrong"},
		},
		{
			name: "fail on none passes an error", dir: erroring, args: []string{"--fail-on=none", "."}, wantCode: exitOK,
			wantStdout: []string{"1 wrong"},
		},
		{
			name: "a file over the limit is left out, said in the summary, and does not fail", dir: limited, args: []string{"config"}, wantCode: exitOK,
			wantExact:  "2 rules, 4 examples: 0 wrong · 1 file left out at the limits\n",
			wantStderr: "config/samples/big.yaml is over the limit of 1048576 bytes, so it is not read",
		},
		{
			name: "a file alone is the workspace", dir: wrong, args: []string{"config/crd/widget.yaml", "config/samples/widget.yaml"}, wantCode: exitOK,
			wantExact: "1 rule, 1 example: 0 wrong\n",
		},
		{
			name: "a path that is missing", dir: good, args: []string{"missing"}, wantCode: exitUsage,
			wantStderr: "examples: read missing: no such file or directory",
		},
		{
			name: "a file that is no YAML", dir: good, args: []string{"notes.txt"}, wantCode: exitUsage,
			wantStderr: "read notes.txt: not a .yaml or .yml file",
		},
		{
			name: "an unknown format", dir: good, args: []string{"--format", "xml"}, wantCode: exitUsage,
			wantStderr: `format "xml": unknown format (want text or json)`,
		},
		{
			name: "an unknown fail-on", dir: good, args: []string{"--fail-on", "warning"}, wantCode: exitUsage,
			wantStderr: `--fail-on: "warning": unknown value (want wrong, error or none)`,
		},
		{
			name: "an unsupported Kubernetes version", dir: good, args: []string{"--kube-version", "1.20"}, wantCode: exitUsage,
			wantStderr: "--kube-version",
		},
		{
			name: "an unknown flag", dir: good, args: []string{"--nope"}, wantCode: exitUsage,
			wantStderr: "flag provided but not defined: -nope",
		},
		{
			name: "no rules", dir: t.TempDir(), wantCode: exitOK,
			wantExact:  "0 rules, 0 examples: 0 wrong\n",
			wantStderr: "warning: no CustomResourceDefinition or ValidatingAdmissionPolicy with rules found in .",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runExamplesBinary(t, tc.dir, tc.args...)
			if code != tc.wantCode {
				t.Errorf("%s examples %q exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", name, tc.args, code, tc.wantCode, stdout, stderr)
			}
			if tc.wantExact != "" && stdout != tc.wantExact {
				t.Errorf("%s examples %q printed\n%s\nwant\n%s", name, tc.args, stdout, tc.wantExact)
			}
			for _, part := range tc.wantStdout {
				if !strings.Contains(stdout, part) {
					t.Errorf("%s examples %q printed\n%s\nwant it to have %q", name, tc.args, stdout, part)
				}
			}
			if tc.wantStderr == "" && stderr != "" || !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("%s examples %q wrote %q to stderr, want %q", name, tc.args, stderr, tc.wantStderr)
			}
			if strings.Contains(stdout, tc.dir) {
				t.Errorf("%s examples %q printed %s, want paths relative to the working folder", name, tc.args, stdout)
			}
		})
	}
}

// TestBinaryExamplesJSON checks that --format json prints one object, the
// shape of cel-lens.exampleResults with the problems of the examples beside
// its rules, whatever --verbose says, and that the exit status is the same as
// for text.
func TestBinaryExamplesJSON(t *testing.T) {
	t.Parallel()
	dir := examplesRepo(t, "self.size <= 10", map[string]string{
		"config/samples/huge.yaml":        widgetExample("20"),
		"config/samples/string-size.yaml": widgetExample(`"three"`),
	})
	for _, tc := range []struct {
		args     []string
		wantCode int
	}{
		{[]string{"--format", "json", "config"}, exitFail},
		{[]string{"--format=json", "--verbose", "--fail-on", "none", "config"}, exitOK},
	} {
		code, stdout, stderr := runExamplesBinary(t, dir, tc.args...)
		if code != tc.wantCode || stderr != "" {
			t.Errorf("%s examples %q = exit %d, stderr %q, want exit %d and none", name, tc.args, code, stderr, tc.wantCode)
		}
		var got struct {
			KubernetesVersion string `json:"kubernetesVersion"`
			Rules             []struct {
				Document string
				Rule     string
				Results  []struct {
					Example struct{ URI, File string }
					Result  string
					Wrong   string
				}
			}
			Problems []struct {
				File         string
				Line, Column int
				Severity     string
				Message      string
			}
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("%s examples %q printed %s: %v", name, tc.args, stdout, err)
		}
		if got.KubernetesVersion != "1.37" || len(got.Rules) != 2 || len(got.Problems) != 1 {
			t.Errorf("%s examples %q printed %s, want Kubernetes 1.37, 2 rules and 1 problem", name, tc.args, stdout)
			continue
		}
		if p := got.Problems[0]; p.File != "samples/string-size.yaml" || p.Line != 4 || p.Severity != "error" || !strings.Contains(p.Message, "must be of type integer") {
			t.Errorf("%s examples %q problem = %+v, want the schema's problem at samples/string-size.yaml:4", name, tc.args, p)
		}
		wrong := 0
		for _, r := range got.Rules {
			for _, res := range r.Results {
				if res.Wrong != "" {
					wrong++
					if res.Example.File != "samples/huge.yaml" || res.Result != "refused" || !strings.HasPrefix(res.Example.URI, "file://") {
						t.Errorf("%s examples %q wrong result = %+v, want samples/huge.yaml refused", name, tc.args, res)
					}
				}
			}
		}
		if wrong != 1 {
			t.Errorf("%s examples %q has %d wrong results, want 1", name, tc.args, wrong)
		}
	}
}

// TestBinaryExamplesUnreadable checks that a CRD, a policy or a binding the
// API server cannot read fails the run, with the problem check gives at the
// word, in text and in JSON, where it was left out without a word before: a
// plain n, which kubectl reads as a boolean, where a string is wanted. A CRD
// with y and n in an enum, which the API server reads, does not fail it.
func TestBinaryExamplesUnreadable(t *testing.T) {
	t.Parallel()
	const wrongType = ": error: %s: expected a string, got a boolean: kubectl reads an unquoted %s as one (YAML 1.1), so quote it if a string is meant\n"
	policy := func(annotationKey, label string) string {
		return strings.Replace(strings.Replace(examplesPolicy, `      message: at most 3 replicas
`, "      message: at most 3 replicas\n  auditAnnotations:\n    - key: "+annotationKey+"\n      valueExpression: \"'x'\"\n", 1), "  validationActions: [Deny]\n", "  validationActions: [Deny]\n  matchResources:\n    namespaceSelector:\n      matchLabels: {team: "+label+"}\n", 1)
	}
	crd := func(extra string) string {
		return strings.Replace(strings.Replace(examplesCRD, "%s", "self.size <= 10", 1), "          type: object\n", "          type: object\n"+extra, 1)
	}
	tests := []struct {
		name     string
		files    map[string]string
		wantCode int
		want     []string // parts of the output
	}{
		{
			name:     "a policy with an annotation key n",
			files:    map[string]string{"policy.yaml": policy("n", "blue")},
			wantCode: exitFail,
			want:     []string{"policy.yaml:12:12" + fmt.Sprintf(wrongType, "spec.auditAnnotations[0].key", "n"), "0 rules, 0 examples: 1 wrong\n"},
		},
		{
			name:     "a binding with a label no",
			files:    map[string]string{"policy.yaml": policy("replicas", "no")},
			wantCode: exitFail,
			want:     []string{"policy.yaml:23:27" + fmt.Sprintf(wrongType, "spec.matchResources.namespaceSelector.matchLabels[team]", "no"), "1 wrong\n"},
		},
		{
			name:     "a CRD with a description n",
			files:    map[string]string{"crd.yaml": crd("          description: n\n"), "widget.yaml": widgetExample("3")},
			wantCode: exitFail,
			want:     []string{"crd.yaml:15:24" + fmt.Sprintf(wrongType, "spec.versions[0].schema.openAPIV3Schema.description", "n"), "1 wrong\n"},
		},
		{
			name:     "a CRD with y and n in an enum",
			files:    map[string]string{"crd.yaml": strings.Replace(crd(""), "                size: {type: integer}", "                size: {type: integer}\n                mode: {type: string, enum: [y, n]}", 1), "widget.yaml": widgetExample("3")},
			wantCode: exitOK,
			want:     []string{"1 rule, 1 example: 0 wrong\n"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, text := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			code, stdout, stderr := runExamplesBinary(t, dir, ".")
			if code != tc.wantCode || strings.Contains(stderr, "found in") && tc.wantCode == exitFail {
				t.Errorf("%s examples . exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", name, code, tc.wantCode, stdout, stderr)
			}
			for _, part := range tc.want {
				if !strings.Contains(stdout, part) {
					t.Errorf("%s examples . printed\n%s\nwant it to have %q", name, stdout, part)
				}
			}
			code, stdout, _ = runExamplesBinary(t, dir, "--format", "json", ".")
			var got struct {
				Problems []struct {
					File         string
					Line, Column int
					Severity     string
					Message      string
				}
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("%s examples --format json . printed %s: %v", name, stdout, err)
			}
			if (len(got.Problems) > 0) != (tc.wantCode == exitFail) || code != tc.wantCode {
				t.Errorf("%s examples --format json . = exit %d with problems %+v, want exit %d", name, code, got.Problems, tc.wantCode)
			}
			for _, p := range got.Problems {
				if p.Severity != "error" || !strings.Contains(p.Message, "expected a string, got a boolean") || p.Line == 0 || p.Column == 0 {
					t.Errorf("%s examples --format json . problem = %+v, want the wrong type at the word", name, p)
				}
			}
		})
	}
}
