var canvas = document.getElementById("game");
var ctx = canvas.getContext("2d");
var nextCanvas = document.getElementById("next");
var nctx = nextCanvas.getContext("2d");
var scoreEl = document.getElementById("score");
var levelEl = document.getElementById("level");
var linesEl = document.getElementById("lines");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");

var COLS = 10, ROWS = 20, CELL = 30;
var SHAPES = [
  [[1,1,1,1]],
  [[1,1],[1,1]],
  [[0,1,0],[1,1,1]],
  [[1,0,0],[1,1,1]],
  [[0,0,1],[1,1,1]],
  [[0,1,1],[1,1,0]],
  [[1,1,0],[0,1,1]]
];
var COLORS = ["#00d4ff","#ffd166","#c77dff","#ff6b35","#2288ff","#06d6a0","#ff3860"];

var board, current, currentX, currentY, currentType, nextType, score, level, totalLines, running, dropTimer, dropInterval, keys = {};

function newPiece(type) { return SHAPES[type].map(function(r) { return r.slice(); }); }

function rotate(piece) {
  var rows = piece.length, cols = piece[0].length;
  var rotated = [];
  for (var c = 0; c < cols; c++) {
    rotated[c] = [];
    for (var r = rows - 1; r >= 0; r--) rotated[c].push(piece[r][c]);
  }
  return rotated;
}

function collides(piece, px, py) {
  for (var r = 0; r < piece.length; r++)
    for (var c = 0; c < piece[r].length; c++)
      if (piece[r][c]) {
        var nx = px + c, ny = py + r;
        if (nx < 0 || nx >= COLS || ny >= ROWS) return true;
        if (ny >= 0 && board[ny][nx] !== -1) return true;
      }
  return false;
}

function lock() {
  for (var r = 0; r < current.length; r++)
    for (var c = 0; c < current[r].length; c++)
      if (current[r][c] && currentY + r >= 0) board[currentY + r][currentX + c] = currentType;
  clearLines();
  spawn();
}

function clearLines() {
  var cleared = 0;
  for (var r = ROWS - 1; r >= 0; r--) {
    if (board[r].every(function(c) { return c !== -1; })) {
      board.splice(r, 1);
      board.unshift(Array(COLS).fill(-1));
      cleared++; r++;
    }
  }
  if (cleared > 0) {
    var points = [0, 100, 300, 500, 800];
    score += (points[cleared] || 800) * level;
    totalLines += cleared;
    level = Math.floor(totalLines / 10) + 1;
    dropInterval = Math.max(50, 500 - (level - 1) * 40);
    updateHUD();
  }
}

function spawn() {
  currentType = nextType;
  nextType = Math.floor(Math.random() * SHAPES.length);
  current = newPiece(currentType);
  currentX = Math.floor((COLS - current[0].length) / 2);
  currentY = -current.length;
  if (collides(current, currentX, 0)) { gameOver(); }
  drawNext();
}

function init() {
  board = [];
  for (var r = 0; r < ROWS; r++) board.push(Array(COLS).fill(-1));
  score = 0; level = 1; totalLines = 0; dropInterval = 500;
  nextType = Math.floor(Math.random() * SHAPES.length);
  spawn();
  updateHUD();
}

function updateHUD() {
  scoreEl.textContent = "Score: " + score;
  levelEl.textContent = "Level: " + level;
  linesEl.textContent = "Lines: " + totalLines;
}

function drawCell(c, x, y, size, alpha) {
  if (c === -1) return;
  var color = COLORS[c];
  ctx.fillStyle = color;
  ctx.globalAlpha = alpha || 1;
  ctx.fillRect(x * size + 1, y * size + 1, size - 2, size - 2);
  ctx.globalAlpha = 0.3;
  ctx.fillStyle = "#fff";
  ctx.fillRect(x * size + 1, y * size + 1, size - 2, 2);
  ctx.fillRect(x * size + 1, y * size + 1, 2, size - 2);
  ctx.globalAlpha = 1;
}

function ghostY() {
  var gy = currentY;
  while (!collides(current, currentX, gy + 1)) gy++;
  return gy;
}

function draw() {
  ctx.fillStyle = "#0d0d2b";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  // Grid
  ctx.strokeStyle = "rgba(42,42,74,0.3)";
  for (var x = 0; x <= COLS; x++) { ctx.beginPath(); ctx.moveTo(x*CELL,0); ctx.lineTo(x*CELL,ROWS*CELL); ctx.stroke(); }
  for (var y = 0; y <= ROWS; y++) { ctx.beginPath(); ctx.moveTo(0,y*CELL); ctx.lineTo(COLS*CELL,y*CELL); ctx.stroke(); }
  // Board
  for (var r = 0; r < ROWS; r++) for (var c = 0; c < COLS; c++) if (board[r][c] !== -1) drawCell(board[r][c], c, r, CELL);
  // Ghost
  var gy = ghostY();
  for (var r = 0; r < current.length; r++)
    for (var c = 0; c < current[r].length; c++)
      if (current[r][c] && gy + r >= 0) drawCell(currentType, currentX + c, gy + r, CELL, 0.2);
  // Current piece
  for (var r = 0; r < current.length; r++)
    for (var c = 0; c < current[r].length; c++)
      if (current[r][c] && currentY + r >= 0) drawCell(currentType, currentX + c, currentY + r, CELL);
}

function drawNext() {
  nctx.fillStyle = "#0d0d2b";
  nctx.fillRect(0, 0, 100, 100);
  var piece = newPiece(nextType);
  var cellSize = 20;
  var ox = Math.floor((100 - piece[0].length * cellSize) / 2);
  var oy = Math.floor((100 - piece.length * cellSize) / 2);
  for (var r = 0; r < piece.length; r++)
    for (var c = 0; c < piece[r].length; c++)
      if (piece[r][c]) {
        nctx.fillStyle = COLORS[nextType];
        nctx.fillRect(ox + c * cellSize + 1, oy + r * cellSize + 1, cellSize - 2, cellSize - 2);
      }
}

function gameOver() {
  running = false;
  msgEl.innerHTML = "Game Over!<br>Score: " + score + "<br>Lines: " + totalLines + "<br><small>Press Space to restart</small>";
  overlay.style.display = "flex";
}

function tick() {
  if (!running) return;
  if (!collides(current, currentX, currentY + 1)) { currentY++; }
  else { lock(); }
  draw();
}

function hardDrop() {
  while (!collides(current, currentX, currentY + 1)) { currentY++; score += 2; }
  lock(); draw(); updateHUD();
}

var lastTick = 0;
function gameLoop(ts) {
  if (!running) return;
  if (ts - lastTick > dropInterval) { tick(); lastTick = ts; }
  // Handle held keys for smooth movement
  draw();
  requestAnimationFrame(gameLoop);
}

function start() {
  init();
  overlay.style.display = "none";
  running = true;
  lastTick = performance.now();
  requestAnimationFrame(gameLoop);
}

document.addEventListener("keydown", function(e) {
  if (["ArrowUp","ArrowDown","ArrowLeft","ArrowRight"," "].indexOf(e.key) >= 0) e.preventDefault();
  if (!running && e.key === " ") { start(); return; }
  if (!running) return;
  if (e.key === "ArrowLeft" && !collides(current, currentX - 1, currentY)) { currentX--; draw(); }
  if (e.key === "ArrowRight" && !collides(current, currentX + 1, currentY)) { currentX++; draw(); }
  if (e.key === "ArrowDown") { if (!collides(current, currentX, currentY + 1)) { currentY++; score++; updateHUD(); draw(); } }
  if (e.key === "ArrowUp") { var rotated = rotate(current); if (!collides(rotated, currentX, currentY)) { current = rotated; draw(); } }
  if (e.key === " ") hardDrop();
});

console.log("Tetris loaded");
init(); draw();