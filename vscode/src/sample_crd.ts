/**
 * The CustomResourceDefinition *Open the Sample CRD* writes, the first step of the *Get started
 * with cel-lens* walkthrough: a `Widget` with three fields and four rules, the pictures' shape. The
 * third rule reads `self.replica`, a field the schema does not have, so the engine shows one
 * problem there and offers *Change to `replicas`* as its quick fix, the second step.
 */
export const SAMPLE_CRD = `# A sample CustomResourceDefinition from the cel-lens walkthrough.
# The line above each rule says what self is and whether the rule compiles.
# The third rule reads self.replica, which the schema does not have: fix it.
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: { plural: widgets, singular: widget, kind: Widget, listKind: WidgetList }
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
                name: { type: string, description: The name of the widget. }
                replicas: { type: integer, description: How many copies of the widget run. }
                minReplicas: { type: integer, description: The fewest copies that may run. }
              x-kubernetes-validations:
                - rule: self.replicas >= self.minReplicas
                  message: replicas must not be below minReplicas
                - rule: self.name.startsWith('w-') && size(self.name) < 20
                  message: the name starts with w- and is shorter than 20 characters
                - rule: self.replica > 0
                  message: a widget runs at least one copy
                - rule: self.replicas <= 100
                  message: a widget runs at most 100 copies
`;

/** Where *Open the Sample CRD* writes the sample, below the extension's storage folder. */
export const SAMPLE_PATH = ['get-started', 'widgets.yaml'] as const;

/** The line of the sample's `x-kubernetes-validations:`, counted from 0: a fresh copy opens there, so its rules are in view. */
export const SAMPLE_RULES_LINE = SAMPLE_CRD.split('\n').findIndex((line) =>
  line.trim().startsWith('x-kubernetes-validations:'),
);
