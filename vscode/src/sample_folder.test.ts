import {
  chmod,
  chown,
  link,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { platform, tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { SAMPLE_CRD } from './sample_crd';
import {
  PRIVATE_FOLDER_TRIES,
  TempSample,
  tempProblem,
  userTempFolder,
  writeSampleInTemp,
} from './sample_folder';

/** Symbolic links and modes need a system with them; Windows's temporary folder is the user's own. */
const POSIX = platform() !== 'win32';
const UID = process.geteuid?.();
/** Root can give a folder to another user, as another user would have made it. */
const ROOT = UID === 0;
const NOBODY = 65534;

let root = '';
/** The user's folder, as `userTempFolder` names it, below a fresh temporary folder. */
let folder = '';
/** A file of the user's outside it, which a planted link points at. */
let victim = '';

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'cel-lens-sample-folder-'));
  folder = userTempFolder(root, UID ?? 'me');
  victim = join(root, 'victim.txt');
  await writeFile(victim, 'keep me');
});

afterEach(async () => {
  await rm(root, { recursive: true, force: true });
});

describe('userTempFolder', () => {
  it.each([
    [1000, '/tmp/cel-lens-1000'],
    ['me', '/tmp/cel-lens-me'],
  ])('names the folder for the user %s', (user, expected) => {
    expect(userTempFolder('/tmp', user).replaceAll('\\', '/')).toBe(expected);
  });
});

describe('writeSampleInTemp', () => {
  it('makes the user’s folder, for the user alone, and writes get-started/widgets.yaml in it', async () => {
    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    const path = join(folder, 'get-started', 'widgets.yaml');
    expect(outcome).toEqual({ written: true, path });
    expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
    if (POSIX) {
      expect((await lstat(folder)).mode & 0o777).toBe(0o700);
      expect((await lstat(join(folder, 'get-started'))).mode & 0o777).toBe(0o700);
      expect((await lstat(path)).mode & 0o077).toBe(0);
    }
  });

  it('writes a fresh copy over an edited one', async () => {
    await writeSampleInTemp(folder, UID, SAMPLE_CRD);
    const path = join(folder, 'get-started', 'widgets.yaml');
    await writeFile(path, `${SAMPLE_CRD}# an edit that is longer than the sample\n`);

    await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
  });

  it.runIf(POSIX)('refuses a folder another user owns, and writes nothing in it', async () => {
    await mkdir(folder, { mode: 0o700 });
    const someoneElse = (UID ?? 0) + 1;

    const outcome = await writeSampleInTemp(folder, someoneElse, SAMPLE_CRD);

    expect(outcome).toEqual({
      written: false,
      reason: `${folder} belongs to another user`,
      fixable: false,
    });
    await expect(lstat(join(folder, 'get-started'))).rejects.toThrow();
  });

  it.runIf(POSIX)('closes a folder of the user’s that others may enter', async () => {
    await mkdir(folder);
    await chmod(folder, 0o777);

    expect((await writeSampleInTemp(folder, UID, SAMPLE_CRD)).written).toBe(true);

    expect((await lstat(folder)).mode & 0o777).toBe(0o700);
  });

  it.runIf(POSIX)('refuses the user’s folder planted as a symbolic link', async () => {
    const elsewhere = join(root, 'elsewhere');
    await mkdir(elsewhere);
    await symlink(elsewhere, folder);

    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(outcome).toEqual({
      written: false,
      reason: `${folder} is a symbolic link`,
      fixable: false,
    });
    await expect(lstat(join(elsewhere, 'get-started'))).rejects.toThrow();
  });

  it.runIf(POSIX)('refuses get-started planted as a symbolic link', async () => {
    const elsewhere = join(root, 'elsewhere');
    await mkdir(elsewhere);
    await mkdir(folder, { mode: 0o700 });
    await symlink(elsewhere, join(folder, 'get-started'));

    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(outcome.written).toBe(false);
    await expect(lstat(join(elsewhere, 'widgets.yaml'))).rejects.toThrow();
  });

  it.runIf(POSIX)(
    'refuses widgets.yaml planted as a symbolic link, and leaves the file it points at alone',
    async () => {
      await mkdir(join(folder, 'get-started'), { recursive: true, mode: 0o700 });
      const path = join(folder, 'get-started', 'widgets.yaml');
      await symlink(victim, path);

      const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

      expect(outcome).toEqual({
        written: false,
        reason: `${path} is a symbolic link`,
        fixable: true,
      });
      expect(await readFile(victim, 'utf8')).toBe('keep me');
    },
  );

  it('refuses a file where the user’s folder should be', async () => {
    await writeFile(folder, 'not a folder');

    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(outcome).toEqual({
      written: false,
      reason: `${folder} is not a folder`,
      fixable: false,
    });
  });

  it('refuses a folder where widgets.yaml should be', async () => {
    await mkdir(join(folder, 'get-started', 'widgets.yaml'), { recursive: true });

    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(outcome.written).toBe(false);
  });
});

describe('writeSampleInTemp, on a file already there', () => {
  it.runIf(POSIX)(
    'refuses a widgets.yaml with another name (a hard link), and leaves the file alone: not the person’s to fix',
    async () => {
      await mkdir(join(folder, 'get-started'), { recursive: true, mode: 0o700 });
      const path = join(folder, 'get-started', 'widgets.yaml');
      await link(victim, path);

      const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

      expect(outcome).toEqual({
        written: false,
        reason: `${path} has another name (a hard link)`,
        fixable: false,
      });
      expect(await readFile(victim, 'utf8')).toBe('keep me');
    },
  );

  it.runIf(ROOT)('refuses a widgets.yaml another user owns, and leaves it alone', async () => {
    await mkdir(join(folder, 'get-started'), { recursive: true, mode: 0o700 });
    const path = join(folder, 'get-started', 'widgets.yaml');
    await writeFile(path, 'theirs');
    await chown(path, NOBODY, NOBODY);

    const outcome = await writeSampleInTemp(folder, UID, SAMPLE_CRD);

    expect(outcome).toEqual({
      written: false,
      reason: `${path} belongs to another user`,
      fixable: false,
    });
    expect(await readFile(path, 'utf8')).toBe('theirs');
  });
});

describe('tempProblem', () => {
  it.runIf(POSIX).each([
    ['777', 0o777, true],
    ['1777', 0o1777, false],
    ['755', 0o755, false],
    ['700', 0o700, false],
  ])('with the temporary folder at mode %s, refuses it: %s', async (_mode, mode, refuses) => {
    await chmod(root, mode);

    expect((await tempProblem(root, UID)) !== undefined).toBe(refuses);
  });

  it.runIf(POSIX).each([
    ['1777', 0o1777, false],
    ['777', 0o777, true],
  ])(
    'judges a temporary folder that is a symbolic link by the folder it names: at mode %s, refuses it: %s',
    async (_mode, mode, refuses) => {
      const real = join(root, 'real-tmp');
      await mkdir(real);
      await chmod(real, mode);
      const linked = join(root, 'tmp-link');
      await symlink(real, linked);

      expect((await tempProblem(linked, UID)) !== undefined).toBe(refuses);
    },
  );

  it('takes any folder where the system has no user ids', async () => {
    await chmod(root, 0o777);

    expect(await tempProblem(root, undefined)).toBeUndefined();
  });
});

describe('TempSample', () => {
  it('writes the sample in the user’s named folder when it can', async () => {
    const sample = new TempSample(root, UID ?? 'me', UID);

    const outcome = await sample.write(SAMPLE_CRD);

    expect(outcome).toEqual({ written: true, path: join(folder, 'get-started', 'widgets.yaml') });
    expect(sample.path).toBe(join(folder, 'get-started', 'widgets.yaml'));
  });

  it.runIf(ROOT)(
    'writes the sample in a new private folder when another user made the named folder first, and keeps it for the session',
    async () => {
      await mkdir(folder, { mode: 0o755 });
      await chown(folder, NOBODY, NOBODY);
      const sample = new TempSample(root, UID ?? 'me', UID);

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome.written).toBe(true);
      const path = sample.path;
      expect(outcome).toEqual({ written: true, path });
      expect(dirname(dirname(path))).not.toBe(folder);
      expect(dirname(dirname(path)).startsWith(`${folder}-`)).toBe(true);
      expect((await lstat(dirname(dirname(path)))).mode & 0o777).toBe(0o700);
      expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
      await expect(lstat(join(folder, 'get-started'))).rejects.toThrow();

      await writeFile(path, 'edited');
      expect(await sample.write(SAMPLE_CRD)).toEqual({ written: true, path });
      expect(await readFile(path, 'utf8')).toBe(SAMPLE_CRD);
    },
  );

  it.runIf(POSIX)(
    'writes the sample in a new private folder when the named folder is a planted link',
    async () => {
      const elsewhere = join(root, 'elsewhere');
      await mkdir(elsewhere);
      await symlink(elsewhere, folder);
      const sample = new TempSample(root, UID ?? 'me', UID);

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome.written).toBe(true);
      expect(dirname(dirname(sample.path)).startsWith(`${folder}-`)).toBe(true);
      expect(await readFile(sample.path, 'utf8')).toBe(SAMPLE_CRD);
      await expect(lstat(join(elsewhere, 'get-started'))).rejects.toThrow();
    },
  );

  it.runIf(POSIX)(
    'moves to a new private folder when widgets.yaml is a hard link, and leaves the linked file alone',
    async () => {
      await mkdir(join(folder, 'get-started'), { recursive: true, mode: 0o700 });
      await link(victim, join(folder, 'get-started', 'widgets.yaml'));
      const sample = new TempSample(root, UID ?? 'me', UID);

      expect((await sample.write(SAMPLE_CRD)).written).toBe(true);

      expect(dirname(dirname(sample.path)).startsWith(`${folder}-`)).toBe(true);
      expect(await readFile(victim, 'utf8')).toBe('keep me');
    },
  );

  it.runIf(POSIX)(
    'stays in the named folder and says the person can fix it when a link is planted inside it',
    async () => {
      await mkdir(join(folder, 'get-started'), { recursive: true, mode: 0o700 });
      await symlink(victim, join(folder, 'get-started', 'widgets.yaml'));
      const sample = new TempSample(root, UID ?? 'me', UID);

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome).toMatchObject({ written: false, fixable: true });
      expect(sample.path).toBe(join(folder, 'get-started', 'widgets.yaml'));
      expect(await readFile(victim, 'utf8')).toBe('keep me');
    },
  );

  it.runIf(POSIX)(
    'writes nothing in a temporary folder every user may write in without the sticky bit',
    async () => {
      await chmod(root, 0o777);
      const sample = new TempSample(root, UID ?? 'me', UID);

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome).toMatchObject({ written: false, fixable: false });
      await expect(lstat(folder)).rejects.toThrow();
    },
  );

  it.runIf(POSIX)(
    'never makes a private folder that is gone again by its name, which others can read: it makes a new one',
    async () => {
      await symlink(join(root, 'elsewhere'), folder);
      const sample = new TempSample(root, UID ?? 'me', UID);
      expect((await sample.write(SAMPLE_CRD)).written).toBe(true);
      const first = dirname(dirname(sample.path));
      await rm(first, { recursive: true });

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome).toEqual({ written: true, path: sample.path });
      const second = dirname(dirname(sample.path));
      expect(second).not.toBe(first);
      expect(second.startsWith(`${folder}-`)).toBe(true);
      await expect(lstat(first)).rejects.toThrow();
      expect(await readFile(sample.path, 'utf8')).toBe(SAMPLE_CRD);
    },
  );

  it.runIf(POSIX)(
    `gives up, not fixable, after ${String(PRIVATE_FOLDER_TRIES)} private folders refused one after another`,
    async () => {
      // A uid that owns nothing here: every folder, the private ones too, reads as another user's.
      const sample = new TempSample(root, 'me', (UID ?? 0) + 1);

      const outcome = await sample.write(SAMPLE_CRD);

      expect(outcome).toMatchObject({ written: false, fixable: false });
      const made = (await readdir(root)).filter((name) => name.startsWith('cel-lens-me-'));
      expect(made).toHaveLength(PRIVATE_FOLDER_TRIES);
    },
  );
});
