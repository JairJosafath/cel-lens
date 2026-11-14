import { constants } from 'node:fs';
import { chmod, lstat, mkdir, mkdtemp, open, stat } from 'node:fs/promises';
import { join } from 'node:path';

import { SAMPLE_PATH } from './sample_crd';

/**
 * Whether the sample was written, or why not. `fixable` says the person can fix it: something in
 * their own folder is in the way, which they can remove. Otherwise the place is not theirs (or
 * not safe) and only another folder helps.
 */
export type SampleWrite =
  | { readonly written: true; readonly path: string }
  | { readonly written: false; readonly reason: string; readonly fixable: boolean };

/**
 * The folder of the system's temporary folder that holds the sample for one user, such as
 * `/tmp/cel-lens-1000`: named for the user, so two people on one machine never share it.
 */
export function userTempFolder(tmp: string, user: number | string): string {
  return join(tmp, `cel-lens-${String(user)}`);
}

/** `O_NOFOLLOW`, which Node leaves out where the system lacks it (Windows). */
const NO_FOLLOW: number | undefined = constants.O_NOFOLLOW;

/** The bits of a folder's mode: any user may write in it, and only an entry's owner may remove it. */
const OTHERS_WRITE = 0o002;
const STICKY = 0o1000;

function code(error: unknown): string | undefined {
  return error instanceof Error && 'code' in error && typeof error.code === 'string'
    ? error.code
    : undefined;
}

function refused(reason: string, fixable: boolean): SampleWrite {
  return { written: false, reason, fixable };
}

/** Makes `path` a folder only its owner can enter, unless it is there already. */
async function makeFolder(path: string): Promise<void> {
  try {
    await mkdir(path, { mode: 0o700 });
  } catch (error) {
    if (code(error) !== 'EEXIST') {
      throw error;
    }
  }
}

/** Why `path` may not hold the sample, and whether the person can fix it; undefined when it may. */
interface FolderProblem {
  readonly reason: string;
  /** Another user's: the person cannot remove it. */
  readonly foreign: boolean;
}

/**
 * Why `path` may not hold the sample: it is a symbolic link or no folder, or (where the system has
 * user ids, `uid` given) another user owns it. A folder of the user that others may enter is
 * closed to them. Undefined when it may.
 */
async function folderProblem(
  path: string,
  uid: number | undefined,
): Promise<FolderProblem | undefined> {
  const found = await lstat(path);
  if (found.isSymbolicLink()) {
    return { reason: `${path} is a symbolic link`, foreign: false };
  }
  if (!found.isDirectory()) {
    return { reason: `${path} is not a folder`, foreign: false };
  }
  if (uid !== undefined && found.uid !== uid) {
    return { reason: `${path} belongs to another user`, foreign: true };
  }
  if (uid !== undefined && (found.mode & 0o077) !== 0) {
    await chmod(path, 0o700);
  }
  return undefined;
}

/**
 * Writes `text` to `path`, which `lstat` found missing or a plain file, never through a link: it
 * opens with `O_NOFOLLOW` (where the system has it) and without truncating, and truncates only a
 * plain file of the user's (`uid`) with no other name (one link). A file another user owns, or one
 * with another name, is not the person's to fix.
 */
async function writeFileSafely(
  path: string,
  uid: number | undefined,
  text: string,
): Promise<SampleWrite> {
  // Without O_NOFOLLOW (Windows) the lstat before stands alone; that temporary folder is the user's own.
  const flags = constants.O_WRONLY | constants.O_CREAT | (NO_FOLLOW ?? 0);
  let handle;
  try {
    handle = await open(path, flags, 0o600);
  } catch (error) {
    if (code(error) === 'ELOOP') {
      return refused(`${path} is a symbolic link`, true);
    }
    throw error;
  }
  try {
    const found = await handle.stat();
    if (!found.isFile()) {
      return refused(`${path} is not a plain file`, true);
    }
    if (uid !== undefined && found.uid !== uid) {
      return refused(`${path} belongs to another user`, false);
    }
    if (found.nlink !== 1) {
      return refused(`${path} has another name (a hard link)`, false);
    }
    await handle.truncate(0);
    await handle.writeFile(text);
  } finally {
    await handle.close();
  }
  return { written: true, path };
}

/**
 * Writes `text` as `get-started/widgets.yaml` below `folder`, a folder of the system's temporary
 * folder, which other users of the machine can also write in. It makes `folder` for this user
 * alone (mode 0700), uses it only when it is a real folder this user owns (`uid`, where the system
 * has user ids), and never writes through a symbolic link: it checks each step with `lstat` and
 * opens the file without following a link. Any other failure throws.
 *
 * On Windows there are no user ids, so nothing checks the owner: `%TEMP%` is the user's own, but
 * a `TMP` that names a shared folder (`C:\Temp`) would let another user make the folder first.
 */
export async function writeSampleInTemp(
  folder: string,
  uid: number | undefined,
  text: string,
  { makeOuter = true }: { readonly makeOuter?: boolean } = {},
): Promise<SampleWrite> {
  const [subfolder, file] = SAMPLE_PATH;
  const inner = join(folder, subfolder);
  const path = join(inner, file);
  if (makeOuter) {
    await makeFolder(folder);
  }
  let outer: FolderProblem | undefined;
  try {
    outer = await folderProblem(folder, uid);
  } catch (error) {
    // A private folder is never made again by its name, which others can read: it is gone.
    if (!makeOuter && code(error) === 'ENOENT') {
      return refused(`${folder} is gone`, false);
    }
    throw error;
  }
  if (outer !== undefined) {
    // An entry of the shared temporary folder that is not the user's folder: in a sticky folder
    // only its owner may remove it, so the person cannot.
    return refused(outer.reason, false);
  }
  await makeFolder(inner);
  const problem = await folderProblem(inner, uid);
  if (problem !== undefined) {
    return refused(problem.reason, !problem.foreign);
  }
  try {
    const found = await lstat(path);
    if (found.isSymbolicLink()) {
      return refused(`${path} is a symbolic link`, true);
    }
    if (!found.isFile()) {
      return refused(`${path} is not a plain file`, true);
    }
  } catch (error) {
    if (code(error) !== 'ENOENT') {
      throw error;
    }
  }
  return writeFileSafely(path, uid, text);
}

/**
 * Why `tmp` is no safe place for the sample: where the system has user ids, a folder every user
 * may write in without the sticky bit lets another user swap a folder there between a check and
 * a write. Undefined when it is safe.
 */
export async function tempProblem(
  tmp: string,
  uid: number | undefined,
): Promise<string | undefined> {
  if (uid === undefined) {
    return undefined;
  }
  // The folder the entries live in: a link to it (macOS's /tmp, a TMPDIR that is a link) is followed.
  const { mode } = await stat(tmp);
  return (mode & OTHERS_WRITE) !== 0 && (mode & STICKY) === 0
    ? `${tmp} lets every user replace what is in it (it is writable by all and not sticky)`
    : undefined;
}

/** How many private folders a write makes, one after another refused, before it gives up. */
export const PRIVATE_FOLDER_TRIES = 3;

/**
 * The sample's place in the system's temporary folder for one session: the user's named folder,
 * or, when that cannot be used (another user made it first, or planted a link there), a new
 * folder with a name nobody can guess (`mkdtemp`, mode 0700), kept for the rest of the session.
 * A private folder is never made again by its name, which anyone may read in the listing: when
 * it is gone (a cleaner of the temporary folder) or refused, the next write makes a new one, up
 * to `PRIVATE_FOLDER_TRIES` times.
 */
export class TempSample {
  private folder: string;
  private ownFolder = false;

  constructor(
    private readonly tmp: string,
    private readonly user: number | string,
    private readonly uid: number | undefined,
  ) {
    this.folder = userTempFolder(tmp, user);
  }

  /** Where the sample is, or goes. */
  get path(): string {
    return join(this.folder, ...SAMPLE_PATH);
  }

  /** Writes `text` as the sample, moving to a private folder when the named one is not the user's. */
  async write(text: string): Promise<SampleWrite> {
    const unsafe = await tempProblem(this.tmp, this.uid);
    if (unsafe !== undefined) {
      return refused(unsafe, false);
    }
    let outcome = await writeSampleInTemp(this.folder, this.uid, text, {
      makeOuter: !this.ownFolder,
    });
    for (let tries = 0; tries < PRIVATE_FOLDER_TRIES; tries += 1) {
      if (outcome.written || outcome.fixable) {
        return outcome;
      }
      this.folder = await mkdtemp(join(this.tmp, `cel-lens-${String(this.user)}-`));
      this.ownFolder = true;
      outcome = await writeSampleInTemp(this.folder, this.uid, text, { makeOuter: false });
    }
    return outcome;
  }
}
