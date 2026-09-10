// WCAG contrast checks for the semantic token pairs in components/tokens.css.
// Text ≥ 4.5:1, control/focus indicators ≥ 3:1. Run: bun test tests/contrast.test.ts

import { describe, expect, test } from "bun:test";

const tokens = await Bun.file(new URL("../prototype/components/tokens.css", import.meta.url)).text();

/** Effective widget palette for a theme: every `:root` block in source order, then the theme block, with var() aliases resolved. */
function palette(themeSelector: string | null): Record<string, string> {
  const blocks = [...tokens.matchAll(/(:root(?:\[[^\]]*\])?)\s*\{([^}]*)\}/g)].map((match) => ({ selector: match[1], body: match[2] }));
  const declarations: Array<[string, string]> = [];
  for (const block of blocks) if (block.selector === ":root") for (const match of block.body.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/gi)) declarations.push([match[1], match[2].trim()]);
  if (themeSelector) for (const block of blocks) if (block.selector === themeSelector) for (const match of block.body.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/gi)) declarations.push([match[1], match[2].trim()]);
  const raw: Record<string, string> = {};
  for (const [name, value] of declarations) raw[name] = value; // last declaration wins, like the cascade
  const resolve = (name: string, depth = 0): string => {
    const value = raw[name]; if (!value || depth > 8) return "";
    const alias = value.match(/^var\((--[a-z0-9-]+)\)$/i);
    return alias ? resolve(alias[1], depth + 1) : value.toLowerCase();
  };
  const out: Record<string, string> = {};
  for (const name of Object.keys(raw)) { const value = resolve(name); if (/^#[0-9a-f]{6}$/.test(value)) out[name] = value; }
  return out;
}

function luminance(hex: string): number {
  const channel = (value: number) => { const c = value / 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
  const r = parseInt(hex.slice(1, 3), 16), g = parseInt(hex.slice(3, 5), 16), b = parseInt(hex.slice(5, 7), 16);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

export function contrast(a: string, b: string): number {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (l1 + 0.05) / (l2 + 0.05);
}

const light = palette(null);
const dark = palette(':root[data-theme="dark"]');

const TEXT_PAIRS: Array<[string, string, string]> = [
  ["body text", "--widget-text", "--widget-surface"],
  ["body text on raised", "--widget-text", "--widget-surface-raised"],
  ["muted metadata", "--widget-text-muted", "--widget-surface"],
  ["muted metadata on raised", "--widget-text-muted", "--widget-surface-raised"],
  ["muted metadata on sunken", "--widget-text-muted", "--widget-surface-sunken"],
  ["warning notice", "--widget-warning-fg", "--widget-warning-bg"],
  ["warning text on surface", "--widget-warning-fg", "--widget-surface"],
  ["danger notice", "--widget-danger-fg", "--widget-danger-bg"],
  ["danger text on surface", "--widget-danger-fg", "--widget-surface"],
  ["positive status", "--widget-positive-fg", "--widget-positive-bg"],
  ["info status", "--widget-info-fg", "--widget-info-bg"],
  ["reference link", "--widget-accent", "--widget-surface"],
  ["reference link on raised", "--widget-accent", "--widget-surface-raised"],
];

// Focus indicators must reach 3:1 against adjacent surfaces (WCAG 1.4.11).
// Decorative 1px card borders are not relied on to identify controls.
const CONTROL_PAIRS: Array<[string, string, string]> = [
  ["focus ring", "--widget-focus", "--widget-surface"],
  ["focus ring on raised", "--widget-focus", "--widget-surface-raised"],
];

for (const [name, colors] of [["light", light], ["dark", dark]] as const) {
  describe(`${name} theme`, () => {
    test("palette parsed", () => { expect(Object.keys(colors).length).toBeGreaterThan(10); });
    for (const [label, fg, bg] of TEXT_PAIRS) test(`${label} ${fg} on ${bg} ≥ 4.5:1`, () => { expect(contrast(colors[fg], colors[bg])).toBeGreaterThanOrEqual(4.5); });
    for (const [label, fg, bg] of CONTROL_PAIRS) test(`${label} ${fg} on ${bg} ≥ 3:1`, () => { expect(contrast(colors[fg], colors[bg])).toBeGreaterThanOrEqual(3); });
  });
}

test("light-theme roles were darkened deliberately: Docket's raw accents fall short on raised surfaces", () => {
  expect(contrast(light["--muted"], light["--panel-2"])).toBeLessThan(4.5);
  expect(contrast(light["--warning"], light["--warning-soft"])).toBeLessThan(4.5);
  expect(contrast(light["--success"], light["--success-soft"])).toBeLessThan(4.5);
});
