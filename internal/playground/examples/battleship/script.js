var pCanvas = document.getElementById("player");
var eCanvas = document.getElementById("enemy");
var pCtx = pCanvas.getContext("2d");
var eCtx = eCanvas.getContext("2d");
var phaseEl = document.getElementById("phase");
var infoEl = document.getElementById("info");
var resetBtn = document.getElementById("reset");
var SZ = 30, GRID = 10;
var SHIPS = [5, 4, 3, 3, 2];
var SHIP_NAMES = ["Carrier", "Battleship", "Cruiser", "Submarine", "Destroyer"];

var playerGrid, enemyGrid, playerShips, enemyShips;
var phase, currentShip, horizontal, hoverR, hoverC;
var playerHits, enemyHits, cpuTargets, cpuHitStack;

function init() {
  playerGrid = makeGrid(); enemyGrid = makeGrid();
  playerShips = []; enemyShips = [];
  phase = "place"; currentShip = 0; horizontal = true;
  hoverR = -1; hoverC = -1;
  playerHits = []; enemyHits = [];
  cpuTargets = []; cpuHitStack = [];
  placeEnemyShips();
  phaseEl.textContent = "Place your ships";
  infoEl.textContent = "Place " + SHIP_NAMES[0] + " (" + SHIPS[0] + " cells) · R to rotate";
  drawPlayer(); drawEnemy();
}

function makeGrid() { var g = []; for (var r = 0; r < GRID; r++) { g[r] = []; for (var c = 0; c < GRID; c++) g[r][c] = 0; } return g; }

function canPlace(grid, r, c, len, horiz) {
  for (var i = 0; i < len; i++) {
    var cr = horiz ? r : r + i; var cc = horiz ? c + i : c;
    if (cr < 0 || cr >= GRID || cc < 0 || cc >= GRID || grid[cr][cc] !== 0) return false;
  } return true;
}

function placeShip(grid, ships, r, c, len, horiz) {
  var cells = [];
  for (var i = 0; i < len; i++) { var cr = horiz ? r : r + i; var cc = horiz ? c + i : c; grid[cr][cc] = 1; cells.push([cr, cc]); }
  ships.push({ cells: cells, sunk: false });
}

function placeEnemyShips() {
  for (var s = 0; s < SHIPS.length; s++) {
    var placed = false;
    while (!placed) {
      var horiz = Math.random() < 0.5;
      var r = Math.floor(Math.random() * GRID);
      var c = Math.floor(Math.random() * GRID);
      if (canPlace(enemyGrid, r, c, SHIPS[s], horiz)) { placeShip(enemyGrid, enemyShips, r, c, SHIPS[s], horiz); placed = true; }
    }
  }
}

function checkSunk(ships, hits) {
  for (var i = 0; i < ships.length; i++) {
    if (ships[i].sunk) continue;
    var allHit = true;
    for (var j = 0; j < ships[i].cells.length; j++) {
      var found = false;
      for (var k = 0; k < hits.length; k++) if (hits[k][0] === ships[i].cells[j][0] && hits[k][1] === ships[i].cells[j][1]) { found = true; break; }
      if (!found) { allHit = false; break; }
    }
    if (allHit) ships[i].sunk = true;
  }
}

function allSunk(ships) { return ships.every(function(s) { return s.sunk; }); }

function wasShot(hits, r, c) { for (var i = 0; i < hits.length; i++) if (hits[i][0] === r && hits[i][1] === c) return true; return false; }

function drawGrid(ctx, grid, hits, showShips, ships) {
  ctx.fillStyle = "#0a1628"; ctx.fillRect(0, 0, 300, 300);
  for (var r = 0; r < GRID; r++) for (var c = 0; c < GRID; c++) {
    ctx.fillStyle = "#0d2137"; ctx.fillRect(c*SZ+1, r*SZ+1, SZ-2, SZ-2);
    ctx.strokeStyle = "#1a3050"; ctx.strokeRect(c*SZ, r*SZ, SZ, SZ);
    if (showShips && grid[r][c] === 1) { ctx.fillStyle = "#1a4a6e"; ctx.fillRect(c*SZ+1, r*SZ+1, SZ-2, SZ-2); }
  }
  // Sunk ships on enemy board
  if (ships && !showShips) {
    for (var i = 0; i < ships.length; i++) {
      if (!ships[i].sunk) continue;
      for (var j = 0; j < ships[i].cells.length; j++) {
        var sr = ships[i].cells[j][0], sc = ships[i].cells[j][1];
        ctx.fillStyle = "#4a1020"; ctx.fillRect(sc*SZ+1, sr*SZ+1, SZ-2, SZ-2);
      }
    }
  }
  for (var i2 = 0; i2 < hits.length; i2++) {
    var hr = hits[i2][0], hc = hits[i2][1]; var isHit = grid[hr][hc] === 1;
    if (isHit) { ctx.fillStyle = "#ff3860"; ctx.beginPath(); ctx.arc(hc*SZ+SZ/2, hr*SZ+SZ/2, 10, 0, Math.PI*2); ctx.fill(); ctx.fillStyle = "#fff"; ctx.font = "bold 14px sans-serif"; ctx.textAlign = "center"; ctx.textBaseline = "middle"; ctx.fillText("X", hc*SZ+SZ/2, hr*SZ+SZ/2); }
    else { ctx.fillStyle = "#335577"; ctx.beginPath(); ctx.arc(hc*SZ+SZ/2, hr*SZ+SZ/2, 5, 0, Math.PI*2); ctx.fill(); }
  }
}

function drawPlayer() { drawGrid(pCtx, playerGrid, enemyHits, true); }
function drawEnemy() { drawGrid(eCtx, enemyGrid, playerHits, false, enemyShips); }

function drawPlacePreview() {
  drawPlayer();
  if (hoverR < 0 || currentShip >= SHIPS.length) return;
  var len = SHIPS[currentShip]; var ok = canPlace(playerGrid, hoverR, hoverC, len, horizontal);
  for (var i = 0; i < len; i++) {
    var cr = horizontal ? hoverR : hoverR + i; var cc = horizontal ? hoverC + i : hoverC;
    if (cr >= 0 && cr < GRID && cc >= 0 && cc < GRID) {
      pCtx.fillStyle = ok ? "rgba(0,212,255,0.3)" : "rgba(255,56,96,0.3)";
      pCtx.fillRect(cc*SZ+1, cr*SZ+1, SZ-2, SZ-2);
    }
  }
}

// CPU AI
function cpuShoot() {
  var r, c;
  if (cpuHitStack.length > 0) {
    var target = cpuHitStack.shift();
    r = target[0]; c = target[1];
  } else {
    do { r = Math.floor(Math.random() * GRID); c = Math.floor(Math.random() * GRID); } while (wasShot(enemyHits, r, c));
  }
  enemyHits.push([r, c]);
  if (playerGrid[r][c] === 1) {
    checkSunk(playerShips, enemyHits);
    var dirs = [[-1,0],[1,0],[0,-1],[0,1]];
    for (var d = 0; d < dirs.length; d++) {
      var nr = r + dirs[d][0], nc = c + dirs[d][1];
      if (nr >= 0 && nr < GRID && nc >= 0 && nc < GRID && !wasShot(enemyHits, nr, nc)) {
        var already = false; for (var i = 0; i < cpuHitStack.length; i++) if (cpuHitStack[i][0] === nr && cpuHitStack[i][1] === nc) already = true;
        if (!already) cpuHitStack.push([nr, nc]);
      }
    }
  }
  // Remove invalid targets
  cpuHitStack = cpuHitStack.filter(function(t) { return !wasShot(enemyHits, t[0], t[1]); });
}

pCanvas.addEventListener("mousemove", function(e) {
  if (phase !== "place") return;
  var rect = pCanvas.getBoundingClientRect();
  hoverC = Math.floor((e.clientX - rect.left) / SZ);
  hoverR = Math.floor((e.clientY - rect.top) / SZ);
  drawPlacePreview();
});

pCanvas.addEventListener("click", function() {
  if (phase !== "place" || currentShip >= SHIPS.length) return;
  var len = SHIPS[currentShip];
  if (!canPlace(playerGrid, hoverR, hoverC, len, horizontal)) return;
  placeShip(playerGrid, playerShips, hoverR, hoverC, len, horizontal);
  currentShip++;
  if (currentShip >= SHIPS.length) {
    phase = "battle"; phaseEl.textContent = "Battle!"; infoEl.textContent = "Click enemy grid to fire";
  } else {
    infoEl.textContent = "Place " + SHIP_NAMES[currentShip] + " (" + SHIPS[currentShip] + " cells) · R to rotate";
  }
  drawPlayer();
});

eCanvas.addEventListener("click", function(e) {
  if (phase !== "battle") return;
  var rect = eCanvas.getBoundingClientRect();
  var c = Math.floor((e.clientX - rect.left) / SZ);
  var r = Math.floor((e.clientY - rect.top) / SZ);
  if (wasShot(playerHits, r, c)) return;
  playerHits.push([r, c]);
  checkSunk(enemyShips, playerHits);
  drawEnemy();
  if (allSunk(enemyShips)) { phase = "over"; phaseEl.textContent = "You win!"; infoEl.textContent = "All enemy ships destroyed"; return; }
  setTimeout(function() {
    cpuShoot(); drawPlayer();
    if (allSunk(playerShips)) { phase = "over"; phaseEl.textContent = "CPU wins!"; infoEl.textContent = "Your fleet was destroyed"; }
  }, 300);
});

document.addEventListener("keydown", function(e) {
  if (e.key === "r" || e.key === "R") { horizontal = !horizontal; if (phase === "place") drawPlacePreview(); }
});

resetBtn.addEventListener("click", init);
console.log("Battleship loaded");
init();