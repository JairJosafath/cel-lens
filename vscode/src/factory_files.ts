import { existsSync } from 'node:fs';

/** Whether the factory's `.gitea/` directory is here; the published source has none. */
export const HAS_FACTORY_WORKFLOWS = existsSync('.gitea/workflows');

/** Names a test that reads the factory's workflows, saying why it skips without them. */
export function factoryTitle(title: string): string {
  return `${title} (skipped when .gitea/ is absent: it is the factory's, not in the published source)`;
}
