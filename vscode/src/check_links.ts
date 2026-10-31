// Fetches every http(s) link in CHANGELOG.md and README.md and fails on any
// response that is not 2xx or 3xx. Run by the releaser through `npm run links`
// before tagging; it needs the network, so `make check` does not run it.
import { readFile } from 'node:fs/promises';
import { argv, exit, stderr, stdout } from 'node:process';
import { fileURLToPath } from 'node:url';

const FILES = ['CHANGELOG.md', 'README.md'];
const LINK = /https?:\/\/[^\s<>()[\]"'`]+/g;
const TRAILING_PUNCTUATION = /[.,;:!?*_]+$/;
const TIMEOUT_MS = 20_000;

export interface LinkResult {
  readonly url: string;
  /** The HTTP status, or the reason the request failed. */
  readonly outcome: number | string;
  readonly ok: boolean;
}

export type Fetcher = (url: string) => Promise<{ readonly status: number }>;

/** Every distinct http(s) link in a Markdown text, in order of first appearance. */
export function extractLinks(markdown: string): string[] {
  const links = (markdown.match(LINK) ?? []).map((link) => link.replace(TRAILING_PUNCTUATION, ''));
  return [...new Set(links)];
}

/** Fetches each link once; a status below 400 passes, anything else or a failed request does not. */
export async function checkLinks(urls: readonly string[], fetcher: Fetcher): Promise<LinkResult[]> {
  return Promise.all(
    urls.map(async (url): Promise<LinkResult> => {
      try {
        const { status } = await fetcher(url);
        return { url, outcome: status, ok: status >= 200 && status < 400 };
      } catch (error) {
        return { url, outcome: error instanceof Error ? error.message : String(error), ok: false };
      }
    }),
  );
}

// Redirects are not followed: a 3xx answer already means the link opens.
const fetchLink: Fetcher = async (url) => {
  const response = await fetch(url, {
    redirect: 'manual',
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
  await response.body?.cancel();
  return response;
};

async function main(): Promise<void> {
  const texts = await Promise.all(FILES.map((file) => readFile(file, 'utf8')));
  const urls = extractLinks(texts.join('\n'));
  const results = await checkLinks(urls, fetchLink);
  for (const { url, outcome, ok } of results) {
    (ok ? stdout : stderr).write(`${ok ? 'ok  ' : 'FAIL'} ${String(outcome)} ${url}\n`);
  }
  const failed = results.filter((result) => !result.ok).length;
  if (failed > 0) {
    throw new Error(
      `links: ${String(failed)} of ${String(results.length)} links in ${FILES.join(' and ')} do not open`,
    );
  }
  stdout.write(`links: all ${String(results.length)} links open\n`);
}

if (argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((error: unknown) => {
    stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    exit(1);
  });
}
