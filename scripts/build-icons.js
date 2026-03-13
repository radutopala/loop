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
const FONT_PATH = '/System/Library/Fonts/SFNS.ttf';
const VARIATION = 'Semibold';

const SVG_FILES = [
  'app/build/icon.svg',
  'app/build/icon-transparent.svg',
];

// Load font once.
let font = fontkit.openSync(FONT_PATH);
if (font.namedVariations && font.namedVariations[VARIATION]) {
  font = font.getVariation(VARIATION);
}

// Cache generated paths by size.
const pathCache = {};

function generatePath(size) {
  if (pathCache[size]) return pathCache[size];

  const run = font.layout(TEXT);
  const scale = size / font.unitsPerEm;

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
  let x1 = Infinity, x2 = -Infinity;
  for (let i = 0; i < nums.length; i += 2) {
    x1 = Math.min(x1, nums[i]);
    x2 = Math.max(x2, nums[i]);
  }

  pathCache[size] = { d, x1, x2, width: x2 - x1 };
  return pathCache[size];
}

// Regex to match the marker block (with leading whitespace on the line).
const MARKER_RE = /([ \t]*)<!-- LOOP_TEXT (.+?) -->\n[\s\S]*?<!-- \/LOOP_TEXT -->/;
const PARAMS_RE = /(\w+)=([\d.]+)/g;

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
    params[m[1]] = parseFloat(m[2]);
  }

  const { size, centerX, baselineY, opacity } = params;
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
    `${indent}<g transform="translate(${translateX}, ${baselineY})" fill="white" opacity="${opacity}">\n` +
    `${indent}  <path d="${p.d}"/>\n` +
    `${indent}</g>\n` +
    `${indent}<!-- /LOOP_TEXT -->`;

  svg = svg.replace(MARKER_RE, replacement);
  fs.writeFileSync(filePath, svg);
  console.log(`${relPath}: ${TEXT} ${size}px → translate(${translateX}, ${baselineY})`);
}
