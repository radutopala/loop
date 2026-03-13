#!/usr/bin/env node
// Generate "Loop" text as SVG vector paths and inject into icon SVG files.
// Uses fontkit for variable font support (SF Pro / SFNS.ttf).
//
// Each SVG file contains marker comments:
//   <!-- LOOP_TEXT size=140 centerX=412 baselineY=595 opacity=0.9 -->
//   <g>...</g>
//   <!-- /LOOP_TEXT -->
//
// This script regenerates the <g> block between markers using fontkit.
//
// Usage: node scripts/build-icons.cjs
// Requires: npm install --prefix /tmp fontkit

const fs = require('fs');
const path = require('path');

let fontkit;
try {
  fontkit = require('fontkit');
} catch {
  try {
    fontkit = require('/tmp/node_modules/fontkit');
  } catch {
    console.error('fontkit not found. Run: npm install --prefix /tmp fontkit');
    process.exit(1);
  }
}

const TEXT = 'Loop';
const FONT_PATH = '/System/Library/Fonts/SFNSRounded.ttf';
const VARIATION = 'Heavy';

const SVG_FILES = [
  'app/build/icon.svg',
  'app/build/icon-transparent.svg',
];

// Load font once.
let font = fontkit.openSync(FONT_PATH);
if (font.namedVariations && font.namedVariations[VARIATION]) {
  font = font.getVariation(VARIATION);
}

// Load a lighter weight for the horizontal app logo.
let fontSemibold = fontkit.openSync(FONT_PATH);
if (fontSemibold.namedVariations && fontSemibold.namedVariations['Semibold']) {
  fontSemibold = fontSemibold.getVariation('Semibold');
}

// Cache generated paths by size+font key.
const pathCache = {};

function generatePath(size, customFont) {
  const key = customFont ? `${size}_custom` : `${size}`;
  if (pathCache[key]) return pathCache[key];

  const f = customFont || font;
  const run = f.layout(TEXT);
  const scale = size / f.unitsPerEm;

  let d = '';
  let x = 0;

  for (let i = 0; i < run.glyphs.length; i++) {
    const glyph = run.glyphs[i];
    const pos = run.positions[i];
    const ox = x + pos.xOffset;

    for (const cmd of glyph.path.commands) {
      switch (cmd.command) {
        case 'moveTo':
          d += `M${((cmd.args[0] + ox) * scale).toFixed(2)} ${(-cmd.args[1] * scale).toFixed(2)}`;
          break;
        case 'lineTo':
          d += `L${((cmd.args[0] + ox) * scale).toFixed(2)} ${(-cmd.args[1] * scale).toFixed(2)}`;
          break;
        case 'quadraticCurveTo':
          d += `Q${((cmd.args[0] + ox) * scale).toFixed(2)} ${(-cmd.args[1] * scale).toFixed(2)} ${((cmd.args[2] + ox) * scale).toFixed(2)} ${(-cmd.args[3] * scale).toFixed(2)}`;
          break;
        case 'bezierCurveTo':
          d += `C${((cmd.args[0] + ox) * scale).toFixed(2)} ${(-cmd.args[1] * scale).toFixed(2)} ${((cmd.args[2] + ox) * scale).toFixed(2)} ${(-cmd.args[3] * scale).toFixed(2)} ${((cmd.args[4] + ox) * scale).toFixed(2)} ${(-cmd.args[5] * scale).toFixed(2)}`;
          break;
        case 'closePath':
          d += 'Z';
          break;
      }
    }
    x += pos.xAdvance;
  }

  // Compute bounding box for centering.
  const nums = d.match(/-?\d+\.?\d*/g).map(Number);
  let x1 = Infinity, x2 = -Infinity, y1 = Infinity, y2 = -Infinity;
  for (let i = 0; i < nums.length; i += 2) {
    x1 = Math.min(x1, nums[i]);
    x2 = Math.max(x2, nums[i]);
    y1 = Math.min(y1, nums[i + 1]);
    y2 = Math.max(y2, nums[i + 1]);
  }

  pathCache[key] = { d, x1, x2, y1, y2, width: x2 - x1, height: y2 - y1 };
  return pathCache[key];
}

// Regex to match the marker block (with leading whitespace on the line).
const MARKER_RE = /([ \t]*)<!-- LOOP_TEXT (.+?) -->\n[\s\S]*?<!-- \/LOOP_TEXT -->/;
const PARAMS_RE = /(\w+)=([\w.#]+)/g;

for (const relPath of SVG_FILES) {
  const filePath = path.resolve(__dirname, '..', relPath);
  let svg = fs.readFileSync(filePath, 'utf-8');

  const match = svg.match(MARKER_RE);
  if (!match) {
    console.warn(`No LOOP_TEXT marker in ${relPath}, skipping`);
    continue;
  }

  const indent = match[1]; // leading whitespace of the marker line

  // Parse params from marker comment.
  const params = {};
  let m;
  while ((m = PARAMS_RE.exec(match[2])) !== null) {
    const val = parseFloat(m[2]);
    params[m[1]] = isNaN(val) ? m[2] : val;
  }

  const { size, centerX, baselineY, opacity, fill } = params;
  const fillColor = fill || 'white';
  if (!size || !centerX || !baselineY || !opacity) {
    console.warn(`Incomplete params in ${relPath}: ${JSON.stringify(params)}, skipping`);
    continue;
  }

  // Generate path and compute centering offset.
  const p = generatePath(size);
  const translateX = Math.round(centerX - (p.x1 + p.width / 2));

  // Build replacement block with consistent indentation.
  const replacement =
    `${indent}<!-- LOOP_TEXT ${match[2]} -->\n` +
    `${indent}<g transform="translate(${translateX}, ${baselineY})" fill="${fillColor}" opacity="${opacity}">\n` +
    `${indent}  <path d="${p.d}"/>\n` +
    `${indent}</g>\n` +
    `${indent}<!-- /LOOP_TEXT -->`;

  svg = svg.replace(MARKER_RE, replacement);
  fs.writeFileSync(filePath, svg);
  console.log(`${relPath}: ${TEXT} ${size}px → translate(${translateX}, ${baselineY})`);
}

// --- Generate standalone logo SVGs for React components ---

const LOGO_COLOR = '#717171';
const INFINITY_D = 'M0 0c-43-57.3-86-86-128.7-86a86 86 0 1 0 0 172c42.7 0 85.7-28.7 128.7-86Zm0 0c43 57.3 86 86 128.7 86a86 86 0 0 0 0-172c-42.7 0-85.7 28.7-128.7 86Z';
const INF_STROKE = 38;
const INF_HALF_W = 214.7;
const INF_HALF_H = 86;
const INF_FULL_W = (INF_HALF_W + INF_STROKE / 2) * 2;
const INF_FULL_H = (INF_HALF_H + INF_STROKE / 2) * 2;

const lt = generatePath(160);
const assetsDir = path.resolve(__dirname, '..', 'app/src/assets');
fs.mkdirSync(assetsDir, { recursive: true });

// Vertical logo: infinity above text (with glow on infinity)
{
  const infScale = (lt.height * 1.1) / INF_FULL_H;
  const sInfW = INF_FULL_W * infScale;
  const sInfH = INF_FULL_H * infScale;
  const gap = lt.height * 0.2;
  const pad = 8; // extra padding for glow bleed
  const totalW = Math.ceil(Math.max(sInfW, lt.width) + pad * 2);
  const totalH = Math.ceil(sInfH + gap + lt.height + pad * 2);
  const infCx = totalW / 2;
  const infCy = sInfH / 2 + pad;
  const textTx = (totalW / 2) - (lt.x1 + lt.width / 2);
  const textTy = sInfH + gap + pad - lt.y1;

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${totalW} ${totalH}">
  <defs>
    <filter id="glow">
      <feGaussianBlur stdDeviation="3" result="blur"/>
      <feMerge>
        <feMergeNode in="blur"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>
  </defs>
  <g transform="translate(${infCx.toFixed(1)}, ${infCy.toFixed(1)}) scale(${infScale.toFixed(4)})" filter="url(#glow)">
    <path d="${INFINITY_D}" fill="none" stroke="${LOGO_COLOR}" stroke-width="${INF_STROKE}" stroke-linecap="round" stroke-linejoin="round"/>
  </g>
  <g transform="translate(${textTx.toFixed(1)}, ${textTy.toFixed(1)})">
    <path d="${lt.d}" fill="${LOGO_COLOR}"/>
  </g>
</svg>\n`;

  fs.writeFileSync(path.join(assetsDir, 'logo-vertical.svg'), svg);
  console.log('app/src/assets/logo-vertical.svg generated');
}

// Horizontal logo: infinity left of text (lighter weight)
{
  const ltH = generatePath(160, fontSemibold);
  const hStroke = 18;
  const hInfFullW = (INF_HALF_W + hStroke / 2) * 2;
  const hInfFullH = (INF_HALF_H + hStroke / 2) * 2;
  const infScale = (ltH.height * 0.55) / hInfFullH;
  const sInfW = hInfFullW * infScale;
  const sInfH = hInfFullH * infScale;
  const gap = ltH.height * 0.25;
  const pad = 2;
  const totalW = Math.ceil(sInfW + gap + ltH.width + pad * 2);
  const totalH = Math.ceil(Math.max(sInfH, ltH.height) + pad * 2);
  const centerY = totalH / 2;
  const infCx = sInfW / 2 + pad;
  const infCy = centerY;
  const textMidY = (ltH.y1 + ltH.y2) / 2;
  const textTx = sInfW + gap + pad - ltH.x1;
  const textTy = centerY - textMidY;

  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${totalW} ${totalH}">
  <g transform="translate(${infCx.toFixed(1)}, ${infCy.toFixed(1)}) scale(${infScale.toFixed(4)})">
    <path d="${INFINITY_D}" fill="none" stroke="${LOGO_COLOR}" stroke-width="${hStroke}" stroke-linecap="round" stroke-linejoin="round"/>
  </g>
  <g transform="translate(${textTx.toFixed(1)}, ${textTy.toFixed(1)})">
    <path d="${ltH.d}" fill="${LOGO_COLOR}"/>
  </g>
</svg>\n`;

  fs.writeFileSync(path.join(assetsDir, 'logo-horizontal.svg'), svg);
  console.log('app/src/assets/logo-horizontal.svg generated');
}
