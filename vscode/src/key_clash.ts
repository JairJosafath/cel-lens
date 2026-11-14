// Whether a shortcut this extension contributes takes one that VS Code or a common extension
// already uses, by the keys `src/taken_keys.json` holds. Pure: `src/keybindings.test.ts` runs it
// on `package.json`.
import { normalizeKey, type TakenSource } from './taken_keys.ts';

/** A keybinding as `contributes.keybindings` has it. */
export interface Keybinding {
  readonly command: string;
  readonly key: string;
  readonly mac?: string;
  readonly when?: string;
}

/** A taken shortcut that a contributed one clashes with. */
export interface Clash {
  /** The source that takes it, such as `VS Code (Linux)` or `Vim`. */
  readonly source: string;
  readonly platform: 'pc' | 'mac';
  /** The contributed key on that platform, normalised. */
  readonly key: string;
  /** The taken key, normalised, and what it runs. */
  readonly taken: string;
  readonly command: string;
  readonly takenWhen: string | undefined;
  /** How they meet: the same key, the taken key is the first part of the contributed one, or the contributed key is the first part of a taken chord. */
  readonly how: 'same key' | 'takes its first part' | 'starts a taken chord';
}

/**
 * Whether a binding can apply while an editor has the focus. A binding whose `when` is `false`, or
 * that applies only with `terminalFocus` or in a `focusedView`, never competes with an editor's
 * binding for a key: the key goes to the terminal or the view, or to nothing. Those are skipped.
 */
export function appliesInEditor(when: string | undefined): boolean {
  if (when === undefined) {
    return true;
  }
  const clause = when.trim();
  return !(
    clause === 'false' ||
    /(?<!!)\bterminalFocus\b/.test(clause) ||
    /(?<!!)\bfocusedView\b/.test(clause) ||
    // The terminal's own accessible view, a terminal key as well.
    /accessibleViewCurrentProviderId\s*==\s*'terminal'/.test(clause)
  );
}

function startsWith(long: readonly string[], short: readonly string[]): boolean {
  return short.length <= long.length && short.every((chord, at) => long[at] === chord);
}

function how(ours: readonly string[], taken: readonly string[]): Clash['how'] | undefined {
  if (ours.length === taken.length && startsWith(ours, taken)) {
    return 'same key';
  }
  if (startsWith(ours, taken)) {
    return 'takes its first part';
  }
  if (startsWith(taken, ours)) {
    return 'starts a taken chord';
  }
  return undefined;
}

/**
 * The taken shortcuts a contributed one clashes with, on each platform: the same key; a taken
 * single key that is its first part (the chord could never start); a single contributed key that
 * starts a taken chord (the chord could never finish). A contributed binding that cannot apply in
 * an editor clashes with nothing, and nor do taken ones that cannot (see `appliesInEditor`).
 */
export function findClashes(binding: Keybinding, sources: readonly TakenSource[]): Clash[] {
  if (!appliesInEditor(binding.when)) {
    return [];
  }
  const clashes: Clash[] = [];
  for (const platform of ['pc', 'mac'] as const) {
    const written = platform === 'mac' ? (binding.mac ?? binding.key) : binding.key;
    const ours = normalizeKey(written);
    for (const source of sources) {
      for (const taken of source[platform]) {
        if (!appliesInEditor(taken.when)) {
          continue;
        }
        const meeting = how(ours, taken.chords);
        if (meeting !== undefined) {
          clashes.push({
            source: source.name,
            platform,
            key: ours.join(' '),
            taken: taken.chords.join(' '),
            command: taken.command,
            takenWhen: taken.when,
            how: meeting,
          });
        }
      }
    }
  }
  return clashes;
}

/** A clash in words, naming the source, the key and its command. */
export function describeClash(binding: Keybinding, clash: Clash): string {
  return `${binding.command} (${clash.platform === 'mac' ? 'macOS' : 'Windows and Linux'}: ${clash.key}) ${clash.how}: ${clash.source} binds ${clash.taken} to ${clash.command}`;
}

function atoms(when: string): string[] | undefined {
  // Only `&&` is understood: a clause with `||` implies nothing it cannot be sure of.
  return when.includes('||') ? undefined : when.split('&&').map((atom) => atom.trim());
}

/**
 * Whether a binding's `when` implies the palette's: every condition the palette asks for is one
 * the binding asks for too, so the command is in the palette wherever the key works. Only `&&`
 * clauses are read.
 */
export function whenImplies(binding: string | undefined, palette: string): boolean {
  if (palette.trim() === 'true') {
    return true;
  }
  const wanted = atoms(palette);
  const given = binding === undefined ? [] : atoms(binding);
  return (
    wanted !== undefined && given !== undefined && wanted.every((atom) => given.includes(atom))
  );
}
