/** A CRD with `count` validation rules, each a realistic CEL expression, for the benches. */
export function generateCrd(count: number): string {
  const rules = Array.from(
    { length: count },
    (_, i) =>
      `    - rule: "self.f${String(i)} <= size(self.g) + 1 && self.all(x, x.matches('^[a-z]+$'))"\n      message: m\n`,
  );
  return `spec:\n  x-kubernetes-validations:\n${rules.join('')}`;
}
