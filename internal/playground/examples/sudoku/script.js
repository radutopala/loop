var boardEl = document.getElementById("board");
var statusEl = document.getElementById("status");
var timerEl = document.getElementById("timer");
var diffSelect = document.getElementById("difficulty");
var newBtn = document.getElementById("new-game");
var hintBtn = document.getElementById("hint");
var solveBtn = document.getElementById("solve");
var numpadEl = document.getElementById("numpad");

var puzzle, solution, grid, given, selectedR, selectedC, timerVal, timerInterval, solved;

function generate() {
  var b = Array(81).fill(0);
  fillBoard(b, 0);
  solution = b.slice();
  var remove = { easy: 35, medium: 45, hard: 55 }[diffSelect.value] || 45;
  puzzle = b.slice();
  var indices = []; for (var i = 0; i < 81; i++) indices.push(i);
  shuffle(indices);
  for (var i = 0; i < remove; i++) puzzle[indices[i]] = 0;
}

function shuffle(arr) { for (var i = arr.length - 1; i > 0; i--) { var j = Math.floor(Math.random() * (i + 1)); var t = arr[i]; arr[i] = arr[j]; arr[j] = t; } }

function fillBoard(b, pos) {
  if (pos >= 81) return true;
  var r = Math.floor(pos / 9), c = pos % 9;
  if (b[pos] !== 0) return fillBoard(b, pos + 1);
  var nums = [1,2,3,4,5,6,7,8,9]; shuffle(nums);
  for (var i = 0; i < 9; i++) {
    if (isValid(b, r, c, nums[i])) { b[pos] = nums[i]; if (fillBoard(b, pos + 1)) return true; b[pos] = 0; }
  }
  return false;
}

function isValid(b, r, c, n) {
  for (var i = 0; i < 9; i++) { if (b[r*9+i] === n || b[i*9+c] === n) return false; }
  var br = Math.floor(r/3)*3, bc = Math.floor(c/3)*3;
  for (var dr = 0; dr < 3; dr++) for (var dc = 0; dc < 3; dc++) if (b[(br+dr)*9+(bc+dc)] === n) return false;
  return true;
}

function init() {
  generate();
  grid = puzzle.slice();
  given = new Set(); for (var i = 0; i < 81; i++) if (puzzle[i] !== 0) given.add(i);
  selectedR = -1; selectedC = -1; solved = false;
  clearInterval(timerInterval); timerVal = 0; timerEl.textContent = "0:00";
  timerInterval = setInterval(function() { if (solved) return; timerVal++; var m = Math.floor(timerVal/60); var s = timerVal%60; timerEl.textContent = m + ":" + (s < 10 ? "0" : "") + s; }, 1000);
  statusEl.textContent = "";
  render();
}

function getConflicts() {
  var conflicts = new Set();
  for (var r = 0; r < 9; r++) for (var c = 0; c < 9; c++) {
    var v = grid[r*9+c]; if (v === 0) continue;
    // Row
    for (var i = 0; i < 9; i++) if (i !== c && grid[r*9+i] === v) { conflicts.add(r*9+c); conflicts.add(r*9+i); }
    // Col
    for (var i2 = 0; i2 < 9; i2++) if (i2 !== r && grid[i2*9+c] === v) { conflicts.add(r*9+c); conflicts.add(i2*9+c); }
    // Box
    var br = Math.floor(r/3)*3, bc = Math.floor(c/3)*3;
    for (var dr = 0; dr < 3; dr++) for (var dc = 0; dc < 3; dc++) { var idx = (br+dr)*9+(bc+dc); if (idx !== r*9+c && grid[idx] === v) { conflicts.add(r*9+c); conflicts.add(idx); } }
  }
  return conflicts;
}

function render() {
  var conflicts = getConflicts();
  boardEl.innerHTML = "";
  for (var r = 0; r < 9; r++) for (var c = 0; c < 9; c++) {
    var idx = r * 9 + c;
    var cell = document.createElement("div");
    cell.className = "cell";
    if (given.has(idx)) cell.classList.add("given");
    else cell.classList.add("user");
    if (r === selectedR && c === selectedC) cell.classList.add("selected");
    else if (selectedR >= 0 && (r === selectedR || c === selectedC || (Math.floor(r/3) === Math.floor(selectedR/3) && Math.floor(c/3) === Math.floor(selectedC/3)))) cell.classList.add("highlight");
    if (conflicts.has(idx) && !given.has(idx)) cell.classList.add("conflict");
    if (grid[idx] !== 0) cell.textContent = grid[idx];
    cell.dataset.r = r; cell.dataset.c = c;
    boardEl.appendChild(cell);
  }
}

function checkWin() {
  if (grid.indexOf(0) >= 0) return false;
  return getConflicts().size === 0;
}

function setCell(n) {
  if (selectedR < 0 || solved) return;
  var idx = selectedR * 9 + selectedC;
  if (given.has(idx)) return;
  grid[idx] = n;
  render();
  if (n > 0 && checkWin()) { solved = true; clearInterval(timerInterval); statusEl.textContent = "Solved in " + timerEl.textContent + "!"; }
}

boardEl.addEventListener("click", function(e) {
  var cell = e.target.closest(".cell"); if (!cell) return;
  selectedR = parseInt(cell.dataset.r); selectedC = parseInt(cell.dataset.c);
  render();
});

document.addEventListener("keydown", function(e) {
  if (e.key >= "1" && e.key <= "9") setCell(parseInt(e.key));
  if (e.key === "Backspace" || e.key === "Delete") setCell(0);
  if (e.key === "ArrowUp" && selectedR > 0) { selectedR--; render(); }
  if (e.key === "ArrowDown" && selectedR < 8) { selectedR++; render(); }
  if (e.key === "ArrowLeft" && selectedC > 0) { selectedC--; render(); }
  if (e.key === "ArrowRight" && selectedC < 8) { selectedC++; render(); }
});

// Numpad
for (var n = 1; n <= 9; n++) {
  var key = document.createElement("div"); key.className = "numkey"; key.textContent = n; key.dataset.n = n;
  key.addEventListener("click", function(e) { setCell(parseInt(e.target.dataset.n)); });
  numpadEl.appendChild(key);
}
var eraseKey = document.createElement("div"); eraseKey.className = "numkey erase"; eraseKey.textContent = "DEL";
eraseKey.addEventListener("click", function() { setCell(0); });
numpadEl.appendChild(eraseKey);

hintBtn.addEventListener("click", function() {
  if (solved) return;
  var empties = [];
  for (var i = 0; i < 81; i++) if (grid[i] === 0) empties.push(i);
  if (empties.length === 0) return;
  var idx = empties[Math.floor(Math.random() * empties.length)];
  grid[idx] = solution[idx]; given.add(idx);
  selectedR = Math.floor(idx / 9); selectedC = idx % 9;
  render();
  var cell = boardEl.children[idx]; cell.classList.add("hint-cell");
  if (checkWin()) { solved = true; clearInterval(timerInterval); statusEl.textContent = "Solved!"; }
});

solveBtn.addEventListener("click", function() {
  if (solved) return;
  for (var i = 0; i < 81; i++) grid[i] = solution[i];
  solved = true; clearInterval(timerInterval);
  statusEl.textContent = "Auto-solved"; render();
});

newBtn.addEventListener("click", init);
console.log("Sudoku loaded");
init();