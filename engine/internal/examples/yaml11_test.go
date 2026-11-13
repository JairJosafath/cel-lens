package examples

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestCheckYAML11 is, for #27, the examples' side: an example with a plain y,
// n, yes, no, on or off where the API server wants a string is refused as it
// refuses the boolean kubectl sends, and the problem tells the author to
// quote it.
func TestCheckYAML11(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	const head = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n"
	for _, word := range []string{"y", "n", "yes", "no", "on", "off", "Yes", "NO", "Off"} {
		hint := fmt.Sprintf("(line %%d: kubectl reads the unquoted %s as a boolean, as YAML 1.1 does; quote it if a string is meant)", word)
		for _, tc := range []struct {
			name, text string
			want       []string // line:column-column message of each problem
		}{
			{
				"a label value", "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n  labels:\n    tier: " + word + "\nspec:\n  size: 1\n",
				[]string{"3:1-9 json: cannot unmarshal bool into Go struct field ObjectMeta.labels of type string " + fmt.Sprintf(hint, 6)},
			},
			{
				"an enum value", head + "spec:\n  size: 1\n  mode: " + word + "\n",
				[]string{
					`7:3-7 spec.mode: Invalid value: "boolean": spec.mode in body must be of type string: "boolean" ` + fmt.Sprintf(hint, 7),
					fmt.Sprintf(`7:3-7 spec.mode: Unsupported value: %t: supported values: "a", "b"`, slices.Contains([]string{"y", "yes", "on", "Yes"}, word)),
				},
			},
		} {
			t.Run(tc.name+"/"+word, func(t *testing.T) {
				t.Parallel()
				objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(tc.text))
				if err != nil || len(objs) == 0 {
					t.Fatalf("Find(%s) = %d examples, %v, want some", tc.name, len(objs), err)
				}
				if err := Check(t.Context(), objs); err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, d := range objs[len(objs)-1].Diagnostics() {
					got = append(got, fmt.Sprintf("%d:%d-%d %s", d.Start.Line, d.Start.Column, d.End.Column, d.Message))
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("Check(%s %s) =\n%s\nwant\n%s", tc.name, word, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
				}
			})
		}
	}
}

// TestCheckYAML11Quoted checks, for #27, that a quoted word is a string to the
// example, which the enum then takes or refuses as it does a string.
func TestCheckYAML11Quoted(t *testing.T) {
	t.Parallel()
	crds := readCRDs(t)
	text := "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n  labels:\n    tier: \"n\"\nspec:\n  size: 1\n  mode: 'yes'\n"
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte(text))
	if err != nil || len(objs) != 1 {
		t.Fatalf("Find() = %d examples, %v, want 1", len(objs), err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range objs[0].Diagnostics() {
		got = append(got, d.Message)
	}
	if want := `spec.mode: Unsupported value: "yes": supported values: "a", "b"`; len(got) != 1 || got[0] != want {
		t.Errorf("Check(quoted words) = %q, want only %q", got, want)
	}
}

// TestReadBooleanWords checks, for #27, that a CRD whose version is served: yes
// and storage: on is read, as the API server reads it, and its examples run:
// it was left out, with every example, for a string where a boolean is wanted.
func TestReadBooleanWords(t *testing.T) {
	t.Parallel()
	crd := strings.NewReplacer("served: true", "served: yes", "storage: true", "storage: on").Replace(widgetCRD)
	if crd == widgetCRD {
		t.Fatal("the test CRD has no served: true to change")
	}
	crds := Read(t.Context(), crdFile, []byte(crd), kubeversion.Default())
	if len(crds) != 1 {
		t.Fatalf("Read(served: yes, storage: on) = %d CRDs, want 1", len(crds))
	}
	objs, err := Find(t.Context(), crds, "/ws/a.yaml", "a.yaml", []byte("apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\nspec:\n  size: 20\n"))
	if err != nil || len(objs) != 1 {
		t.Fatalf("Find() = %d examples, %v, want 1", len(objs), err)
	}
	if err := Check(t.Context(), objs); err != nil {
		t.Fatal(err)
	}
	if d := objs[0].Diagnostics(); len(d) != 1 || !strings.Contains(d[0].Message, "should be less than or equal to 10") {
		t.Errorf("Check(example of the CRD) = %v, want the schema's maximum, which ran", d)
	}
}
