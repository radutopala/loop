var ROWS = 16, COLS = 16, MINES = 40;
var board, revealed, flagged, mineCount, gameOver, firstClick, timerVal, timerInterval;
var boardEl = document.getElementById("board");
var minesEl = document.getElementById("mines");
var timerEl = document.getElementById("timer");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");
var resetBtn = document.getElementById("reset");

function init() {
  board = []; revealed = []; flagged = [];
  for (var r = 0; r < ROWS; r++) {
    board[r] = []; revealed[r] = []; flagged[r] = [];
    for (var c = 0; c < COLS; c++) { board[r][c] = 0; revealed[r][c] = false; flagged[r][c] = false; }
  }
  mineCount = MINES; gameOver = false; firstClick = true;
  timerVal = 0; clearInterval(timerInterval);
  timerEl.textContent = "Time: 0";
  minesEl.textContent = "Mines: " + MINES;
  overlay.style.display = "none";
  render();
}

function placeMines(safeR, safeC) {
  var placed = 0;
  while (placed < MINES) {
    var r = Math.floor(Math.random() * ROWS), c = Math.floor(Math.random() * COLS);
    if (board[r][c] === -1) continue;
    if (Math.abs(r - safeR) <= 1 && Math.abs(c - safeC) <= 1) continue;
    board[r][c] = -1; placed++;
  }
  for (var r = 0; r < ROWS; r++) for (var c = 0; c < COLS; c++) {
    if (board[r][c] === -1) continue;
    var count = 0;
    for (var dr = -1; dr <= 1; dr++) for (var dc = -1; dc <= 1; dc++) {
      var nr = r+dr, nc = c+dc;
      if (nr >= 0 && nr < ROWS && nc >= 0 && nc < COLS && board[nr][nc] === -1) count++;
    }
    board[r][c] = count;
  }
}

function reveal(r, c) {
  if (r < 0 || r >= ROWS || c < 0 || c >= COLS || revealed[r][c] || flagged[r][c]) return;
  revealed[r][c] = true;
  if (board[r][c] === 0) {
    for (var dr = -1; dr <= 1; dr++) for (var dc = -1; dc <= 1; dc++) reveal(r+dr, c+dc);
  }
}

function checkWin() {
  var unrevealed = 0;
  for (var r = 0; r < ROWS; r++) for (var c = 0; c < COLS; c++) if (!revealed[r][c]) unrevealed++;
  return unrevealed === MINES;
}

function render() {
  boardEl.style.gridTemplateColumns = "repeat(" + COLS + ", 28px)";
  boardEl.innerHTML = "";
  for (var r = 0; r < ROWS; r++) {
    for (var c = 0; c < COLS; c++) {
      var cell = document.createElement("div");
      cell.className = "cell";
      cell.dataset.r = r; cell.dataset.c = c;
      if (revealed[r][c]) {
        cell.classList.add("revealed");
        if (board[r][c] === -1) { cell.classList.add("mine"); cell.textContent = "*"; }
        else if (board[r][c] > 0) { cell.textContent = board[r][c]; cell.classList.add("n" + board[r][c]); }
      } else {
        cell.classList.add("hidden");
        if (flagged[r][c]) cell.classList.add("flagged");
      }
      boardEl.appendChild(cell);
    }
  }
}

boardEl.addEventListener("click", function(e) {
  var cell = e.target.closest(".cell");
  if (!cell || gameOver) return;
  var r = parseInt(cell.dataset.r), c = parseInt(cell.dataset.c);
  if (flagged[r][c] || revealed[r][c]) return;
  if (firstClick) { placeMines(r, c); firstClick = false; timerInterval = setInterval(function() { timerVal++; timerEl.textContent = "Time: " + timerVal; }, 1000); }
  if (board[r][c] === -1) {
    gameOver = true; clearInterval(timerInterval);
    for (var rr = 0; rr < ROWS; rr++) for (var cc = 0; cc < COLS; cc++) if (board[rr][cc] === -1) revealed[rr][cc] = true;
    render();
    msgEl.textContent = "Game Over!"; overlay.style.display = "flex";
    return;
  }
  reveal(r, c);
  render();
  if (checkWin()) {
    gameOver = true; clearInterval(timerInterval);
    msgEl.textContent = "You Win! Time: " + timerVal + "s"; overlay.style.display = "flex";
  }
});

boardEl.addEventListener("contextmenu", function(e) {
  e.preventDefault();
  var cell = e.target.closest(".cell");
  if (!cell || gameOver) return;
  var r = parseInt(cell.dataset.r), c = parseInt(cell.dataset.c);
  if (revealed[r][c]) return;
  flagged[r][c] = !flagged[r][c];
  mineCount += flagged[r][c] ? -1 : 1;
  minesEl.textContent = "Mines: " + mineCount;
  render();
});

resetBtn.addEventListener("click", init);

console.log("Minesweeper loaded");
init();