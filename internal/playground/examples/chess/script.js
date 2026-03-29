import { Chess } from "chess.js";

var canvas = document.getElementById("board");
var ctx = canvas.getContext("2d");
var turnEl = document.getElementById("turn");
var statusEl = document.getElementById("status");
var resetBtn = document.getElementById("reset");
var undoBtn = document.getElementById("undo");
var movesEl = document.getElementById("moves");
var SZ = 60;

var UNICODE = { K: "\u2654", Q: "\u2655", R: "\u2656", B: "\u2657", N: "\u2658", P: "\u2659", k: "\u265A", q: "\u265B", r: "\u265C", b: "\u265D", n: "\u265E", p: "\u265F" };

var game, selected, highlights, cpuThinking;

function init() {
  game = new Chess();
  selected = null; highlights = []; cpuThinking = false;
  statusEl.textContent = ""; movesEl.textContent = "";
  updateStatus(); draw();
}

function sq(r, c) { return String.fromCharCode(97 + c) + (8 - r); }
function fromSq(s) { return [8 - parseInt(s[1]), s.charCodeAt(0) - 97]; }

function updateStatus() {
  if (game.isCheckmate()) { statusEl.textContent = (game.turn() === "w" ? "Black" : "White") + " wins by checkmate!"; turnEl.textContent = "Game Over"; }
  else if (game.isDraw()) {
    var reason = "Draw";
    if (game.isStalemate()) reason = "Stalemate";
    else if (game.isThreefoldRepetition()) reason = "Threefold repetition";
    else if (game.isInsufficientMaterial()) reason = "Insufficient material";
    statusEl.textContent = reason + "!"; turnEl.textContent = "Game Over";
  }
  else if (game.isCheck()) { statusEl.textContent = "Check!"; turnEl.textContent = game.turn() === "w" ? "White to move" : "Black to move"; }
  else { statusEl.textContent = ""; turnEl.textContent = game.turn() === "w" ? "White to move" : "Black to move"; }
  // Move history
  var history = game.history();
  var parts = [];
  for (var i = 0; i < history.length; i += 2) {
    var num = Math.floor(i / 2) + 1;
    parts.push(num + ". " + history[i] + (history[i + 1] ? " " + history[i + 1] : ""));
  }
  movesEl.textContent = parts.join("  ");
  movesEl.scrollTop = movesEl.scrollHeight;
}

function draw() {
  for (var r = 0; r < 8; r++) for (var c = 0; c < 8; c++) {
    ctx.fillStyle = (r + c) % 2 === 0 ? "#f0d9b5" : "#b58863";
    ctx.fillRect(c * SZ, r * SZ, SZ, SZ);
  }
  // Last move highlight
  var hist = game.history({ verbose: true });
  if (hist.length > 0) {
    var last = hist[hist.length - 1];
    var f = fromSq(last.from); var t = fromSq(last.to);
    ctx.fillStyle = "rgba(255,255,0,0.15)";
    ctx.fillRect(f[1]*SZ, f[0]*SZ, SZ, SZ);
    ctx.fillRect(t[1]*SZ, t[0]*SZ, SZ, SZ);
  }
  // Highlights
  for (var i = 0; i < highlights.length; i++) {
    var h = highlights[i];
    ctx.fillStyle = "rgba(0,200,100,0.3)";
    ctx.fillRect(h[1]*SZ, h[0]*SZ, SZ, SZ);
    var piece = game.get(sq(h[0], h[1]));
    if (piece && piece.color !== game.turn()) {
      ctx.fillStyle = "rgba(255,0,0,0.25)";
      ctx.fillRect(h[1]*SZ, h[0]*SZ, SZ, SZ);
    } else if (!piece) {
      ctx.fillStyle = "rgba(0,0,0,0.12)";
      ctx.beginPath(); ctx.arc(h[1]*SZ+SZ/2, h[0]*SZ+SZ/2, 8, 0, Math.PI*2); ctx.fill();
    }
  }
  // Selected
  if (selected) { ctx.fillStyle = "rgba(0,150,255,0.4)"; ctx.fillRect(selected[1]*SZ, selected[0]*SZ, SZ, SZ); }
  // Check
  if (game.isCheck()) {
    var board = game.board();
    for (var r2 = 0; r2 < 8; r2++) for (var c2 = 0; c2 < 8; c2++) {
      var p = board[r2][c2];
      if (p && p.type === "k" && p.color === game.turn()) { ctx.fillStyle = "rgba(255,0,0,0.4)"; ctx.fillRect(c2*SZ, r2*SZ, SZ, SZ); }
    }
  }
  // Pieces
  var board2 = game.board();
  ctx.textAlign = "center"; ctx.textBaseline = "middle";
  for (var r3 = 0; r3 < 8; r3++) for (var c3 = 0; c3 < 8; c3++) {
    var p2 = board2[r3][c3];
    if (!p2) continue;
    var key = p2.color === "w" ? p2.type.toUpperCase() : p2.type;
    var ch = UNICODE[key];
    ctx.font = (SZ - 10) + "px serif";
    ctx.fillStyle = p2.color === "w" ? "#fff" : "#222";
    ctx.strokeStyle = p2.color === "w" ? "#333" : "#fff";
    ctx.lineWidth = 0.5;
    ctx.fillText(ch, c3*SZ+SZ/2, r3*SZ+SZ/2+2);
    ctx.strokeText(ch, c3*SZ+SZ/2, r3*SZ+SZ/2+2);
  }
  // Coordinates
  ctx.font = "9px sans-serif";
  for (var i2 = 0; i2 < 8; i2++) {
    ctx.fillStyle = i2 % 2 === 0 ? "#b58863" : "#f0d9b5";
    ctx.textAlign = "left"; ctx.fillText(8 - i2, 2, i2*SZ+11);
    ctx.fillStyle = (7+i2) % 2 === 0 ? "#b58863" : "#f0d9b5";
    ctx.textAlign = "center"; ctx.fillText(String.fromCharCode(97+i2), i2*SZ+SZ/2, 8*SZ-3);
  }
}

// --- CPU AI ---
var VALS = { p: 100, n: 320, b: 330, r: 500, q: 900, k: 20000 };
var PST = {
  p: [0,0,0,0,0,0,0,0, 50,50,50,50,50,50,50,50, 10,10,20,30,30,20,10,10, 5,5,10,25,25,10,5,5, 0,0,0,20,20,0,0,0, 5,-5,-10,0,0,-10,-5,5, 5,10,10,-20,-20,10,10,5, 0,0,0,0,0,0,0,0],
  n: [-50,-40,-30,-30,-30,-30,-40,-50, -40,-20,0,0,0,0,-20,-40, -30,0,10,15,15,10,0,-30, -30,5,15,20,20,15,5,-30, -30,0,15,20,20,15,0,-30, -30,5,10,15,15,10,5,-30, -40,-20,0,5,5,0,-20,-40, -50,-40,-30,-30,-30,-30,-40,-50],
  b: [-20,-10,-10,-10,-10,-10,-10,-20, -10,0,0,0,0,0,0,-10, -10,0,10,10,10,10,0,-10, -10,5,5,10,10,5,5,-10, -10,0,10,10,10,10,0,-10, -10,10,10,10,10,10,10,-10, -10,5,0,0,0,0,5,-10, -20,-10,-10,-10,-10,-10,-10,-20],
  r: [0,0,0,0,0,0,0,0, 5,10,10,10,10,10,10,5, -5,0,0,0,0,0,0,-5, -5,0,0,0,0,0,0,-5, -5,0,0,0,0,0,0,-5, -5,0,0,0,0,0,0,-5, -5,0,0,0,0,0,0,-5, 0,0,0,5,5,0,0,0],
  q: [-20,-10,-10,-5,-5,-10,-10,-20, -10,0,0,0,0,0,0,-10, -10,0,5,5,5,5,0,-10, -5,0,5,5,5,5,0,-5, 0,0,5,5,5,5,0,-5, -10,5,5,5,5,5,0,-10, -10,0,5,0,0,0,0,-10, -20,-10,-10,-5,-5,-10,-10,-20],
  k: [-30,-40,-40,-50,-50,-40,-40,-30, -30,-40,-40,-50,-50,-40,-40,-30, -30,-40,-40,-50,-50,-40,-40,-30, -30,-40,-40,-50,-50,-40,-40,-30, -20,-30,-30,-40,-40,-30,-30,-20, -10,-20,-20,-20,-20,-20,-20,-10, 20,20,0,0,0,0,20,20, 20,30,10,0,0,10,30,20],
};

function evaluate() {
  var board = game.board(); var score = 0;
  for (var r = 0; r < 8; r++) for (var c = 0; c < 8; c++) {
    var p = board[r][c]; if (!p) continue;
    var v = VALS[p.type] || 0;
    var pstIdx = p.color === "w" ? r * 8 + c : (7 - r) * 8 + c;
    var pst = PST[p.type] ? PST[p.type][pstIdx] : 0;
    score += p.color === "w" ? (v + pst) : -(v + pst);
  }
  return score;
}

function minimax(depth, alpha, beta, maximizing) {
  if (depth === 0 || game.isGameOver()) {
    if (game.isCheckmate()) return maximizing ? -99999 : 99999;
    if (game.isDraw()) return 0;
    return evaluate();
  }
  var moves = game.moves();
  // Move ordering: captures first
  moves.sort(function(a, b) { var ac = a.includes("x") ? 0 : 1; var bc = b.includes("x") ? 0 : 1; return ac - bc; });
  if (maximizing) {
    var best = -Infinity;
    for (var i = 0; i < moves.length; i++) { game.move(moves[i]); var val = minimax(depth - 1, alpha, beta, false); game.undo(); best = Math.max(best, val); alpha = Math.max(alpha, val); if (beta <= alpha) break; }
    return best;
  } else {
    var best2 = Infinity;
    for (var i2 = 0; i2 < moves.length; i2++) { game.move(moves[i2]); var val2 = minimax(depth - 1, alpha, beta, true); game.undo(); best2 = Math.min(best2, val2); beta = Math.min(beta, val2); if (beta <= alpha) break; }
    return best2;
  }
}

function cpuMove() {
  cpuThinking = true; statusEl.textContent = "Thinking..."; draw();
  setTimeout(function() {
    var moves = game.moves();
    if (moves.length === 0) { updateStatus(); cpuThinking = false; draw(); return; }
    var bestScore = Infinity, bestMove = moves[0];
    moves.sort(function(a, b) { var ac = a.includes("x") ? 0 : 1; var bc = b.includes("x") ? 0 : 1; return ac - bc; });
    for (var i = 0; i < moves.length; i++) {
      game.move(moves[i]);
      var val = minimax(2, -Infinity, Infinity, true);
      game.undo();
      if (val < bestScore) { bestScore = val; bestMove = moves[i]; }
    }
    game.move(bestMove);
    cpuThinking = false; selected = null; highlights = [];
    updateStatus(); draw();
  }, 50);
}

canvas.addEventListener("click", function(e) {
  if (game.isGameOver() || game.turn() !== "w" || cpuThinking) return;
  var rect = canvas.getBoundingClientRect();
  var c = Math.floor((e.clientX - rect.left) / SZ);
  var r = Math.floor((e.clientY - rect.top) / SZ);
  if (r < 0 || r > 7 || c < 0 || c > 7) return;
  var clickSq = sq(r, c);

  if (selected) {
    var fromSqStr = sq(selected[0], selected[1]);
    // Check for promotion
    var piece = game.get(fromSqStr);
    var isPromo = piece && piece.type === "p" && ((piece.color === "w" && r === 0) || (piece.color === "b" && r === 7));
    try {
      var move = game.move({ from: fromSqStr, to: clickSq, promotion: isPromo ? "q" : undefined });
      if (move) { selected = null; highlights = []; updateStatus(); draw(); if (!game.isGameOver()) cpuMove(); return; }
    } catch(ex) {}
  }

  // Select
  var p = game.get(clickSq);
  if (p && p.color === "w") {
    selected = [r, c];
    var legalMoves = game.moves({ square: clickSq, verbose: true });
    highlights = legalMoves.map(function(m) { return fromSq(m.to); });
  } else { selected = null; highlights = []; }
  draw();
});

undoBtn.addEventListener("click", function() {
  if (cpuThinking || game.history().length < 2) return;
  game.undo(); game.undo(); // Undo both CPU and player move
  selected = null; highlights = [];
  updateStatus(); draw();
});

resetBtn.addEventListener("click", init);
console.log("Chess (chess.js) loaded");
init();