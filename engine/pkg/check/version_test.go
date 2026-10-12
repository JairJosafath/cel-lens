package check_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
	"k8s.io/apiserver/pkg/util/compatibility"
)

// listsCRD uses the semver library, which an API server accepts in new expressions from 1.34 on.
const listsCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            nums:
              type: array
              maxItems: 10
              items:
                type: integer
          x-kubernetes-validations:
            - rule: isSemver('1.2.3')
`

const includesPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  validations:
    - expression: "[1, 2].includes([1])"
`

const listsPolicy = `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
spec:
  validations:
    - expression: "isSemver('1.2.3')"
`

func TestCheckVersionedFunctions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
	}{
		{"CRD rule", listsCRD},
		{"policy expression", listsPolicy},
	}
	for _, tt := range tests {
		for _, ver := range []string{"1.33", "1.34", "1.35", "1.36", "1.37"} {
			t.Run(tt.name+" "+ver, func(t *testing.T) {
				t.Parallel()
				v, err := kubeversion.Parse(ver)
				if err != nil {
					t.Fatal(err)
				}
				got, err := check.Check(context.Background(), "f.yaml", []byte(tt.src), check.WithVersion(v))
				if err != nil {
					t.Fatalf("Check(%s) error = %v", ver, err)
				}
				if ver != "1.33" {
					if len(got) != 0 {
						t.Errorf("Check(%s) = %v, want no problem: semver exists in %s", ver, got, ver)
					}
					return
				}
				if len(got) != 1 || !strings.Contains(got[0].Message, "isSemver") {
					t.Errorf("Check(%s) = %v, want one problem about isSemver: it is not in %s", ver, got, ver)
				}
			})
		}
	}
}

func TestCheckDefaultVersion(t *testing.T) {
	t.Parallel()
	got, err := check.Check(context.Background(), "f.yaml", []byte(listsCRD))
	if err != nil || len(got) != 0 {
		t.Errorf("Check(default) = %v, %v, want no problem: the default is 1.37", got, err)
	}
}

// TestCheckVersionsTogether runs the versions at once, as a server with many
// documents does; -race finds unguarded shared state.
func TestCheckVersionsTogether(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for i := range 8 {
		ver := kubeversion.Supported()[i%len(kubeversion.Supported())]
		wg.Go(func() {
			got, err := check.Check(context.Background(), "f.yaml", []byte(listsCRD), check.WithVersion(ver))
			want := 0
			if ver.String() == "1.33" {
				want = 1
			}
			if err != nil || len(got) != want {
				errs <- fmt.Sprintf("Check(%s) = %v, %v, want %d problems", ver, got, err, want)
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestDefaultIsServerMinCompatibility pins the default to what a 1.37 API
// server compiles new expressions with: the minimum compatibility version of
// the libraries' build, which environment.DefaultCompatibilityVersion reads
// from a setting that Check changes while it runs.
func TestDefaultIsServerMinCompatibility(t *testing.T) {
	t.Parallel()
	if got, want := kubeversion.Default().Compat(), compatibility.DefaultBuildEffectiveVersion().MinCompatibilityVersion(); !got.EqualTo(want) {
		t.Errorf("Default().Compat() = %v, want %v", got, want)
	}
	got, err := check.Check(context.Background(), "f.yaml", []byte(includesPolicy))
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Message, "includes") {
		t.Errorf("Check(default, includes) = %v, %v, want one problem about includes", got, err)
	}
}
