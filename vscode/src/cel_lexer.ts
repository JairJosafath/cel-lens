export type CelTokenKind =
  'variable' | 'field' | 'function' | 'keyword' | 'number' | 'string' | 'operator' | 'punctuation';

/** A token as UTF-16 offsets into the source: `start` included, `end` excluded. */
export interface CelLexeme {
  kind: CelTokenKind;
  start: number;
  end: number;
}

const KEYWORDS = new Set(['true', 'false', 'null', 'in']);
const LONGEST_KEYWORD = 5;
const OPERATOR_PAIRS = new Set(['==', '!=', '<=', '>=', '&&', '||']);
const OPERATOR_CHAR_CODES = new Set(Array.from('<>!?:+-*/%', (char) => char.charCodeAt(0)));
const PUNCTUATION_CHAR_CODES = new Set(Array.from('()[]{},.', (char) => char.charCodeAt(0)));
const NUMBER = /(?:0[xX][0-9a-fA-F]+[uU]?|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?[uU]?)/y;

const STRING_PREFIX_CODES = new Set([66, 82, 98, 114]); // B R b r
const SPACE = 32;
const QUOTE_DOUBLE = 34;
const QUOTE_SINGLE = 39;
const BACKSLASH = 92;
const DOT = 46;
const OPEN_PAREN = 40;
const EQUALS = 61;
const AMPERSAND = 38;
const PIPE = 124;

function isDigit(code: number): boolean {
  return code >= 48 && code <= 57;
}

function isIdentifierStart(code: number): boolean {
  return (code >= 65 && code <= 90) || (code >= 97 && code <= 122) || code === 95;
}

function isIdentifierPart(code: number): boolean {
  return isIdentifierStart(code) || isDigit(code);
}

function isWhitespace(code: number): boolean {
  return code === SPACE || (code >= 9 && code <= 13) || code === 0xa0 || code === 0xfeff;
}

function isQuote(code: number): boolean {
  return code === QUOTE_DOUBLE || code === QUOTE_SINGLE;
}

function skipSpaces(source: string, from: number): number {
  let at = from;
  while (at < source.length && isWhitespace(source.charCodeAt(at))) {
    at++;
  }
  return at;
}

/** The end of the string literal that starts at `start` (after any prefix). */
function stringEnd(source: string, start: number, raw: boolean): number {
  const quote = source.charAt(start);
  const triple = source.startsWith(quote.repeat(3), start);
  const delimiter = triple ? quote.repeat(3) : quote;
  let at = start + delimiter.length;
  while (at < source.length) {
    const code = source.charCodeAt(at);
    if (source.startsWith(delimiter, at)) {
      return at + delimiter.length;
    }
    if (!raw && code === BACKSLASH) {
      at += 2;
    } else if (!triple && (code === 10 || code === 13)) {
      return at; // a quoted string ends at its line: the rest is still tokenized
    } else {
      at++;
    }
  }
  return Math.min(at, source.length);
}

/** The length of a string prefix (`r`, `b`, `rb`, ...) at `at` when a quote follows it, else 0. */
function prefixLength(source: string, at: number): number {
  if (!STRING_PREFIX_CODES.has(source.charCodeAt(at))) {
    return 0;
  }
  let end = at;
  while (end < at + 2 && 'rRbB'.includes(source.charAt(end))) {
    end++;
  }
  return end > at && isQuote(source.charCodeAt(end)) ? end - at : 0;
}

/** Whether a two-character string prefix has an `r` as its second character (the first is checked by the caller). */
function isRawCode(second: number, length: number): boolean {
  return length > 1 && (second === 82 || second === 114);
}

/** Called for each token: its kind and its `start` (included) and `end` (excluded) offsets. */
export type LexemeSink = (kind: CelTokenKind, start: number, end: number) => void;

/** Splits CEL into tokens. It never throws: see `lexCel`. Tokens go to `emit` one by one, in order. */
export function scanCel(source: string, emit: LexemeSink): void {
  const { length } = source;
  let afterDot = false;
  let at = 0;
  while (at < length) {
    const code = source.charCodeAt(at);
    if (isWhitespace(code)) {
      at++;
      continue;
    }
    const prefix = STRING_PREFIX_CODES.has(code) ? prefixLength(source, at) : 0;
    let kind: CelTokenKind | undefined;
    let end = at + 1;
    if (prefix > 0) {
      const raw = code === 82 || code === 114 || isRawCode(source.charCodeAt(at + 1), prefix);
      end = stringEnd(source, at + prefix, raw);
      kind = 'string';
    } else if (isQuote(code)) {
      end = stringEnd(source, at, false);
      kind = 'string';
    } else if (isDigit(code) || (code === DOT && isDigit(source.charCodeAt(at + 1)))) {
      NUMBER.lastIndex = at;
      end = at + (NUMBER.exec(source)?.[0].length ?? 1);
      kind = 'number';
    } else if (isIdentifierStart(code)) {
      while (end < length && isIdentifierPart(source.charCodeAt(end))) {
        end++;
      }
      const word = end - at <= LONGEST_KEYWORD ? source.slice(at, end) : '';
      if (KEYWORDS.has(word)) {
        kind = 'keyword';
      } else if (source.charCodeAt(skipSpaces(source, end)) === OPEN_PAREN) {
        kind = 'function';
      } else {
        kind = afterDot ? 'field' : 'variable';
      }
    } else if (PUNCTUATION_CHAR_CODES.has(code)) {
      kind = 'punctuation';
    } else if (
      OPERATOR_CHAR_CODES.has(code) ||
      code === EQUALS ||
      code === AMPERSAND ||
      code === PIPE
    ) {
      const next = source.charCodeAt(at + 1);
      const pair =
        (next === EQUALS || next === AMPERSAND || next === PIPE) &&
        OPERATOR_PAIRS.has(source.slice(at, at + 2));
      if (pair || OPERATOR_CHAR_CODES.has(code)) {
        kind = 'operator';
        end = at + (pair ? 2 : 1);
      }
    }
    if (kind === undefined) {
      at++;
      continue;
    }
    afterDot = kind === 'punctuation' && code === DOT;
    emit(kind, at, end);
    at = end;
  }
}

/**
 * Splits CEL into tokens. It never throws: an unterminated string runs to the end
 * of its line (or of the text, if triple-quoted) and an unknown character is skipped.
 * The extension streams tokens with `scanCel`; this list form is the entry point of the lexer tests.
 */
export function lexCel(source: string): CelLexeme[] {
  const lexemes: CelLexeme[] = [];
  scanCel(source, (kind, start, end) => lexemes.push({ kind, start, end }));
  return lexemes;
}
