'use strict';

// A small TOML reader: enough for herdrmon's config.toml and for reading the
// parts of Herdr's config.toml this plugin cares about (theme name, custom
// colours, which sidebar tables exist). No dependencies, no writer.
//
// Supported: comments, [table] and [[array.of.tables]] headers with dotted
// and quoted keys, dotted keys in assignments, basic and literal strings
// (single- and multi-line), integers, floats, booleans, arrays (multi-line,
// nested) and inline tables. Dates are kept as strings. Anything else throws
// a TomlError with the line number.

class TomlError extends Error {
  constructor(message, line) {
    super(`toml: line ${line}: ${message}`);
    this.line = line;
  }
}

function parse(text) {
  const root = {};
  let current = root;
  let i = 0;
  let line = 1;
  const src = String(text).replace(/\r\n/g, '\n');

  const fail = (msg) => {
    throw new TomlError(msg, line);
  };
  const peek = () => src[i];
  const eof = () => i >= src.length;

  function skipWs() {
    while (!eof() && (src[i] === ' ' || src[i] === '\t')) i++;
  }
  function skipComment() {
    if (src[i] === '#') while (!eof() && src[i] !== '\n') i++;
  }
  // Whitespace, newlines and comments, as allowed inside arrays.
  function skipAll() {
    for (;;) {
      skipWs();
      skipComment();
      if (src[i] === '\n') {
        i++;
        line++;
        continue;
      }
      break;
    }
  }
  function endOfLine() {
    skipWs();
    skipComment();
    if (!eof() && src[i] !== '\n') fail(`unexpected ${JSON.stringify(src[i])}`);
  }

  function basicString() {
    if (src.startsWith('"""', i)) {
      i += 3;
      if (src[i] === '\n') {
        i++;
        line++;
      }
      let out = '';
      for (;;) {
        if (eof()) fail('unterminated string');
        if (src.startsWith('"""', i) && !src.startsWith('""""', i)) {
          i += 3;
          return out;
        }
        if (src[i] === '\\') {
          if (src[i + 1] === '\n' || /^\\[ \t]*\n/.test(src.slice(i, i + 64))) {
            // Line-ending backslash: trim the newline and leading whitespace.
            i++;
            while (!eof() && /\s/.test(src[i])) {
              if (src[i] === '\n') line++;
              i++;
            }
            continue;
          }
          out += escape();
          continue;
        }
        if (src[i] === '\n') line++;
        out += src[i++];
      }
    }
    i++;
    let out = '';
    for (;;) {
      if (eof() || src[i] === '\n') fail('unterminated string');
      if (src[i] === '"') {
        i++;
        return out;
      }
      if (src[i] === '\\') {
        out += escape();
        continue;
      }
      out += src[i++];
    }
  }
  function escape() {
    const c = src[i + 1];
    i += 2;
    const simple = { b: '\b', t: '\t', n: '\n', f: '\f', r: '\r', '"': '"', '\\': '\\', e: '\x1b' };
    if (c in simple) return simple[c];
    if (c === 'u' || c === 'U') {
      const n = c === 'u' ? 4 : 8;
      const hex = src.slice(i, i + n);
      if (!/^[0-9a-fA-F]+$/.test(hex) || hex.length !== n) fail('bad unicode escape');
      i += n;
      return String.fromCodePoint(parseInt(hex, 16));
    }
    return fail(`bad escape \\${c}`);
  }
  function literalString() {
    if (src.startsWith("'''", i)) {
      i += 3;
      if (src[i] === '\n') {
        i++;
        line++;
      }
      const end = src.indexOf("'''", i);
      if (end < 0) fail('unterminated string');
      let stop = end;
      while (src[stop + 3] === "'") stop++;
      const out = src.slice(i, stop);
      line += (out.match(/\n/g) ?? []).length;
      i = stop + 3;
      return out;
    }
    i++;
    const end = src.indexOf("'", i);
    const nl = src.indexOf('\n', i);
    if (end < 0 || (nl >= 0 && nl < end)) fail('unterminated string');
    const out = src.slice(i, end);
    i = end + 1;
    return out;
  }

  function key() {
    skipWs();
    if (peek() === '"') return basicString();
    if (peek() === "'") return literalString();
    const m = /^[A-Za-z0-9_-]+/.exec(src.slice(i, i + 256));
    if (!m) fail('expected a key');
    i += m[0].length;
    return m[0];
  }
  function dottedKey() {
    const parts = [key()];
    for (;;) {
      skipWs();
      if (peek() !== '.') return parts;
      i++;
      parts.push(key());
    }
  }

  function value() {
    skipWs();
    const c = peek();
    if (c === '"') return basicString();
    if (c === "'") return literalString();
    if (c === '[') return array();
    if (c === '{') return inlineTable();
    const m = /^[^\s,\]}#]+/.exec(src.slice(i, i + 128));
    if (!m) fail('expected a value');
    const raw = m[0];
    i += raw.length;
    if (raw === 'true') return true;
    if (raw === 'false') return false;
    if (/^[+-]?(inf|nan)$/.test(raw)) return raw.endsWith('inf') ? (raw[0] === '-' ? -Infinity : Infinity) : NaN;
    const clean = raw.replace(/_/g, '');
    if (/^[+-]?\d+$/.test(clean)) return Number(clean);
    if (/^0x[0-9a-fA-F]+$/.test(clean)) return parseInt(clean.slice(2), 16);
    if (/^0o[0-7]+$/.test(clean)) return parseInt(clean.slice(2), 8);
    if (/^0b[01]+$/.test(clean)) return parseInt(clean.slice(2), 2);
    if (/^[+-]?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(clean)) return Number(clean);
    // Dates and times: keep the text, possibly with a space-separated time.
    if (/^\d{4}-\d{2}-\d{2}/.test(raw) || /^\d{2}:\d{2}/.test(raw)) {
      const t = /^ \d{2}:\d{2}[^\s,\]}#]*/.exec(src.slice(i, i + 64));
      if (t) {
        i += t[0].length;
        return raw + t[0];
      }
      return raw;
    }
    return fail(`bad value ${JSON.stringify(raw)}`);
  }
  function array() {
    i++;
    const out = [];
    for (;;) {
      skipAll();
      if (peek() === ']') {
        i++;
        return out;
      }
      out.push(value());
      skipAll();
      if (peek() === ',') {
        i++;
        continue;
      }
      if (peek() === ']') {
        i++;
        return out;
      }
      fail('expected , or ] in array');
    }
  }
  function inlineTable() {
    i++;
    const out = {};
    skipWs();
    if (peek() === '}') {
      i++;
      return out;
    }
    for (;;) {
      const k = dottedKey();
      skipWs();
      if (peek() !== '=') fail('expected =');
      i++;
      assign(out, k, value());
      skipWs();
      if (peek() === ',') {
        i++;
        continue;
      }
      if (peek() === '}') {
        i++;
        return out;
      }
      fail('expected , or } in inline table');
    }
  }

  function assign(table, keys, v) {
    let t = table;
    for (const k of keys.slice(0, -1)) {
      if (t[k] === undefined) t[k] = {};
      else if (typeof t[k] !== 'object' || Array.isArray(t[k])) fail(`key ${k} is not a table`);
      t = t[k];
    }
    const last = keys[keys.length - 1];
    if (Object.prototype.hasOwnProperty.call(t, last)) fail(`duplicate key ${last}`);
    t[last] = v;
  }

  function header() {
    const arrayOf = src.startsWith('[[', i);
    i += arrayOf ? 2 : 1;
    const keys = dottedKey();
    skipWs();
    if (!src.startsWith(arrayOf ? ']]' : ']', i)) fail('unterminated table header');
    i += arrayOf ? 2 : 1;
    endOfLine();
    let t = root;
    keys.forEach((k, n) => {
      const last = n === keys.length - 1;
      if (last && arrayOf) {
        if (t[k] === undefined) t[k] = [];
        if (!Array.isArray(t[k])) fail(`${k} is not an array of tables`);
        const fresh = {};
        t[k].push(fresh);
        t = fresh;
        return;
      }
      if (t[k] === undefined) t[k] = {};
      // Walking through an array of tables descends into its last element.
      if (Array.isArray(t[k])) t = t[k][t[k].length - 1];
      else if (typeof t[k] === 'object') t = t[k];
      else fail(`${k} is not a table`);
    });
    current = t;
  }

  while (!eof()) {
    skipAll();
    if (eof()) break;
    if (peek() === '[') {
      header();
      continue;
    }
    const k = dottedKey();
    skipWs();
    if (peek() !== '=') fail('expected =');
    i++;
    assign(current, k, value());
    endOfLine();
  }
  return root;
}

module.exports = { parse, TomlError };
