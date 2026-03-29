var canvas = document.getElementById("game");
var ctx = canvas.getContext("2d");
var W, H;
function resize() { W = canvas.width = window.innerWidth; H = canvas.height = window.innerHeight; }
resize(); window.addEventListener("resize", resize);

var scoreEl = document.getElementById("score");
var levelEl = document.getElementById("level");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");

var GRAVITY = 0.6, JUMP = -12, SPEED = 5, TILE = 40;
var player, platforms, coins, spikes, camX, score, levelNum, running, keys = {};

function generateLevel(num) {
  platforms = []; coins = []; spikes = [];
  var y = H - 80, x = 0;
  // Ground start
  for (var i = 0; i < 8; i++) platforms.push({ x: x + i * TILE, y: y, w: TILE, h: TILE });
  x = 8 * TILE;
  var totalWidth = 3000 + num * 500;
  while (x < totalWidth) {
    var gap = Math.random() < 0.3 ? TILE * (2 + Math.floor(Math.random() * 2)) : 0;
    x += gap;
    var platLen = 2 + Math.floor(Math.random() * 5);
    var platY = y - Math.floor(Math.random() * 4) * TILE;
    platY = Math.max(100, Math.min(H - 80, platY));
    for (var i = 0; i < platLen; i++) {
      platforms.push({ x: x + i * TILE, y: platY, w: TILE, h: TILE });
      if (Math.random() < 0.3) coins.push({ x: x + i * TILE + TILE/2, y: platY - 30, r: 8, collected: false });
      if (Math.random() < 0.1 && i > 0) spikes.push({ x: x + i * TILE, y: platY - 16, w: TILE, h: 16 });
    }
    x += platLen * TILE;
    y = platY;
  }
  // Goal
  platforms.push({ x: totalWidth, y: H - 120, w: TILE * 3, h: TILE * 3, goal: true });
}

function init() {
  player = { x: 60, y: H - 140, vx: 0, vy: 0, w: 20, h: 28, onGround: false, dead: false };
  score = 0; levelNum = 1; camX = 0;
  generateLevel(levelNum);
  updateHUD();
}

function updateHUD() {
  scoreEl.textContent = "Coins: " + score;
  levelEl.textContent = "Level: " + levelNum;
}

function draw() {
  // Sky gradient
  var grad = ctx.createLinearGradient(0, 0, 0, H);
  grad.addColorStop(0, "#0a0a2e"); grad.addColorStop(1, "#1a1a4e");
  ctx.fillStyle = grad; ctx.fillRect(0, 0, W, H);

  ctx.save();
  ctx.translate(-camX, 0);

  // Platforms
  for (var i = 0; i < platforms.length; i++) {
    var p = platforms[i];
    if (p.x + p.w < camX - 50 || p.x > camX + W + 50) continue;
    if (p.goal) {
      ctx.fillStyle = "#ffd166"; ctx.shadowColor = "#ffd166"; ctx.shadowBlur = 15;
      ctx.fillRect(p.x, p.y, p.w, p.h);
      ctx.shadowBlur = 0;
      ctx.fillStyle = "#0a0a2e"; ctx.font = "bold 14px monospace"; ctx.textAlign = "center";
      ctx.fillText("EXIT", p.x + p.w/2, p.y + p.h/2 + 5);
    } else {
      ctx.fillStyle = "#16213e";
      ctx.fillRect(p.x, p.y, p.w, p.h);
      ctx.strokeStyle = "#1a3050"; ctx.strokeRect(p.x, p.y, p.w, p.h);
    }
  }

  // Spikes
  ctx.fillStyle = "#ff3860";
  for (var i = 0; i < spikes.length; i++) {
    var s = spikes[i];
    if (s.x + s.w < camX - 50 || s.x > camX + W + 50) continue;
    ctx.beginPath();
    ctx.moveTo(s.x, s.y + s.h); ctx.lineTo(s.x + s.w/2, s.y); ctx.lineTo(s.x + s.w, s.y + s.h);
    ctx.closePath(); ctx.fill();
  }

  // Coins
  ctx.fillStyle = "#ffd166"; ctx.shadowColor = "#ffd166"; ctx.shadowBlur = 8;
  for (var i = 0; i < coins.length; i++) {
    var c = coins[i];
    if (c.collected || c.x < camX - 50 || c.x > camX + W + 50) continue;
    ctx.beginPath(); ctx.arc(c.x, c.y + Math.sin(Date.now() * 0.005 + i) * 3, c.r, 0, Math.PI * 2); ctx.fill();
  }
  ctx.shadowBlur = 0;

  // Player
  if (!player.dead) {
    ctx.fillStyle = "#06d6a0"; ctx.shadowColor = "#06d6a0"; ctx.shadowBlur = 8;
    ctx.fillRect(player.x, player.y, player.w, player.h);
    // Eyes
    ctx.shadowBlur = 0;
    ctx.fillStyle = "#fff";
    ctx.fillRect(player.x + 4, player.y + 6, 5, 5);
    ctx.fillRect(player.x + 12, player.y + 6, 5, 5);
    ctx.fillStyle = "#111";
    ctx.fillRect(player.x + 6, player.y + 8, 2, 2);
    ctx.fillRect(player.x + 14, player.y + 8, 2, 2);
  }

  ctx.restore();
}

function step() {
  if (!running) return;
  // Movement
  if (keys["ArrowLeft"] || keys["a"]) player.vx = -SPEED;
  else if (keys["ArrowRight"] || keys["d"]) player.vx = SPEED;
  else player.vx *= 0.7;
  if ((keys[" "] || keys["ArrowUp"] || keys["w"]) && player.onGround) { player.vy = JUMP; player.onGround = false; }

  player.vy += GRAVITY;
  player.x += player.vx; player.y += player.vy;
  player.onGround = false;

  // Platform collision
  for (var i = 0; i < platforms.length; i++) {
    var p = platforms[i];
    if (player.x + player.w > p.x && player.x < p.x + p.w && player.y + player.h > p.y && player.y + player.h < p.y + p.h + 12 && player.vy >= 0) {
      player.y = p.y - player.h;
      player.vy = 0;
      player.onGround = true;
      if (p.goal) { nextLevel(); return; }
    }
  }

  // Coin collection
  for (var i = 0; i < coins.length; i++) {
    var c = coins[i];
    if (c.collected) continue;
    var dx = (player.x + player.w/2) - c.x, dy = (player.y + player.h/2) - c.y;
    if (Math.sqrt(dx*dx + dy*dy) < c.r + 14) { c.collected = true; score++; updateHUD(); }
  }

  // Spike collision
  for (var i = 0; i < spikes.length; i++) {
    var s = spikes[i];
    if (player.x + player.w > s.x + 4 && player.x < s.x + s.w - 4 && player.y + player.h > s.y + 4 && player.y < s.y + s.h) { die(); return; }
  }

  // Fall death
  if (player.y > H + 100) { die(); return; }

  // Camera
  camX += (player.x - W * 0.35 - camX) * 0.1;
  if (camX < 0) camX = 0;

  draw();
  requestAnimationFrame(step);
}

function die() {
  player.dead = true; running = false;
  msgEl.innerHTML = "You died!<br>Coins: " + score + "<br><small>Press Space to restart</small>";
  overlay.style.display = "flex";
}

function nextLevel() {
  levelNum++;
  player.x = 60; player.y = H - 140; player.vx = 0; player.vy = 0; camX = 0;
  generateLevel(levelNum);
  updateHUD();
}

function start() {
  init();
  overlay.style.display = "none";
  running = true;
  requestAnimationFrame(step);
}

document.addEventListener("keydown", function(e) {
  keys[e.key] = true;
  if (["ArrowUp","ArrowDown","ArrowLeft","ArrowRight"," "].indexOf(e.key) >= 0) e.preventDefault();
  if (!running && e.key === " ") start();
});
document.addEventListener("keyup", function(e) { keys[e.key] = false; });

console.log("Platformer loaded");
init(); draw();