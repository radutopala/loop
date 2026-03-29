var canvas = document.getElementById("game");
var ctx = canvas.getContext("2d");
var W, H;
function resize() { W = canvas.width = window.innerWidth; H = canvas.height = window.innerHeight; }
resize();
window.addEventListener("resize", resize);

var scoreEl = document.getElementById("score");
var livesEl = document.getElementById("lives");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");

var keys = {};
var ship, bullets, asteroids, particles, score, lives, running, invuln, invulnTimer;

function init() {
  ship = { x: W/2, y: H/2, angle: -Math.PI/2, vx: 0, vy: 0, thrust: false };
  bullets = []; asteroids = []; particles = [];
  score = 0; lives = 3; invuln = false; invulnTimer = 0;
  for (var i = 0; i < 6; i++) spawnAsteroid(3);
  updateHUD();
}

function spawnAsteroid(size, x, y) {
  if (x === undefined) {
    var edge = Math.floor(Math.random() * 4);
    if (edge === 0) { x = 0; y = Math.random() * H; }
    else if (edge === 1) { x = W; y = Math.random() * H; }
    else if (edge === 2) { x = Math.random() * W; y = 0; }
    else { x = Math.random() * W; y = H; }
  }
  var speed = (4 - size) * 0.8 + Math.random() * 1.5;
  var angle = Math.random() * Math.PI * 2;
  var radii = [];
  var baseR = size * 15;
  for (var i = 0; i < 10; i++) radii.push(baseR + (Math.random() - 0.5) * baseR * 0.6);
  asteroids.push({ x: x, y: y, vx: Math.cos(angle)*speed, vy: Math.sin(angle)*speed, size: size, radii: radii, rot: Math.random()*Math.PI*2, rotSpeed: (Math.random()-0.5)*0.04 });
}

function updateHUD() {
  scoreEl.textContent = "Score: " + score;
  livesEl.textContent = "Lives: " + lives;
}

function wrap(obj) {
  if (obj.x < -50) obj.x += W + 100;
  if (obj.x > W + 50) obj.x -= W + 100;
  if (obj.y < -50) obj.y += H + 100;
  if (obj.y > H + 50) obj.y -= H + 100;
}

function explode(x, y, count, color) {
  for (var i = 0; i < count; i++) {
    var a = Math.random() * Math.PI * 2;
    var s = Math.random() * 3 + 1;
    particles.push({ x: x, y: y, vx: Math.cos(a)*s, vy: Math.sin(a)*s, life: 30 + Math.random()*20, color: color || "#fff" });
  }
}

var shootCd = 0;

function step() {
  if (!running) return;
  ctx.fillStyle = "#000";
  ctx.fillRect(0, 0, W, H);

  // Ship controls
  if (keys["ArrowLeft"] || keys["a"]) ship.angle -= 0.06;
  if (keys["ArrowRight"] || keys["d"]) ship.angle += 0.06;
  if (keys["ArrowUp"] || keys["w"]) {
    ship.vx += Math.cos(ship.angle) * 0.15;
    ship.vy += Math.sin(ship.angle) * 0.15;
    ship.thrust = true;
  } else { ship.thrust = false; }
  // Friction
  ship.vx *= 0.995; ship.vy *= 0.995;
  var maxSpeed = 8;
  var spd = Math.sqrt(ship.vx*ship.vx + ship.vy*ship.vy);
  if (spd > maxSpeed) { ship.vx *= maxSpeed/spd; ship.vy *= maxSpeed/spd; }
  ship.x += ship.vx; ship.y += ship.vy;
  wrap(ship);

  if (shootCd > 0) shootCd--;
  if (keys[" "] && shootCd === 0) {
    bullets.push({ x: ship.x + Math.cos(ship.angle)*16, y: ship.y + Math.sin(ship.angle)*16, vx: Math.cos(ship.angle)*8+ship.vx*0.3, vy: Math.sin(ship.angle)*8+ship.vy*0.3, life: 50 });
    shootCd = 10;
  }

  // Invulnerability
  if (invuln) { invulnTimer--; if (invulnTimer <= 0) invuln = false; }

  // Draw ship
  if (!invuln || Math.floor(Date.now()/80) % 2) {
    ctx.save();
    ctx.translate(ship.x, ship.y);
    ctx.rotate(ship.angle);
    ctx.strokeStyle = "#fff"; ctx.lineWidth = 1.5;
    ctx.beginPath();
    ctx.moveTo(16, 0); ctx.lineTo(-10, -10); ctx.lineTo(-6, 0); ctx.lineTo(-10, 10); ctx.closePath();
    ctx.stroke();
    if (ship.thrust) {
      ctx.strokeStyle = "#ff6b35";
      ctx.beginPath();
      ctx.moveTo(-6, -4); ctx.lineTo(-14 - Math.random()*6, 0); ctx.lineTo(-6, 4);
      ctx.stroke();
    }
    ctx.restore();
  }

  // Update bullets
  ctx.fillStyle = "#fff";
  for (var i = bullets.length - 1; i >= 0; i--) {
    var b = bullets[i];
    b.x += b.vx; b.y += b.vy; b.life--;
    wrap(b);
    if (b.life <= 0) { bullets.splice(i, 1); continue; }
    ctx.beginPath(); ctx.arc(b.x, b.y, 2, 0, Math.PI*2); ctx.fill();
  }

  // Update asteroids
  ctx.strokeStyle = "#888"; ctx.lineWidth = 1.5;
  for (var i = asteroids.length - 1; i >= 0; i--) {
    var a = asteroids[i];
    a.x += a.vx; a.y += a.vy; a.rot += a.rotSpeed;
    wrap(a);
    // Draw
    ctx.save(); ctx.translate(a.x, a.y); ctx.rotate(a.rot);
    ctx.beginPath();
    for (var j = 0; j < a.radii.length; j++) {
      var ang = (j / a.radii.length) * Math.PI * 2;
      var rx = Math.cos(ang) * a.radii[j], ry = Math.sin(ang) * a.radii[j];
      if (j === 0) ctx.moveTo(rx, ry); else ctx.lineTo(rx, ry);
    }
    ctx.closePath(); ctx.stroke();
    ctx.restore();

    // Bullet collision
    var hitR = a.size * 15;
    for (var j = bullets.length - 1; j >= 0; j--) {
      var b = bullets[j];
      var dx = b.x - a.x, dy = b.y - a.y;
      if (Math.sqrt(dx*dx + dy*dy) < hitR) {
        explode(a.x, a.y, 8, "#aaa");
        score += (4 - a.size) * 50;
        updateHUD();
        bullets.splice(j, 1);
        if (a.size > 1) { spawnAsteroid(a.size - 1, a.x, a.y); spawnAsteroid(a.size - 1, a.x, a.y); }
        asteroids.splice(i, 1);
        break;
      }
    }
  }

  // Ship-asteroid collision
  if (!invuln) {
    for (var i = 0; i < asteroids.length; i++) {
      var a = asteroids[i];
      var dx = ship.x - a.x, dy = ship.y - a.y;
      if (Math.sqrt(dx*dx + dy*dy) < a.size * 15 + 10) {
        explode(ship.x, ship.y, 20, "#ff6b35");
        lives--;
        updateHUD();
        if (lives <= 0) { gameOver(); return; }
        ship.x = W/2; ship.y = H/2; ship.vx = 0; ship.vy = 0;
        invuln = true; invulnTimer = 120;
        break;
      }
    }
  }

  // Respawn asteroids
  if (asteroids.length === 0) { for (var i = 0; i < 6 + Math.floor(score / 500); i++) spawnAsteroid(3); }

  // Update particles
  for (var i = particles.length - 1; i >= 0; i--) {
    var p = particles[i];
    p.x += p.vx; p.y += p.vy; p.life--;
    ctx.globalAlpha = p.life / 50;
    ctx.fillStyle = p.color;
    ctx.fillRect(p.x, p.y, 2, 2);
    if (p.life <= 0) particles.splice(i, 1);
  }
  ctx.globalAlpha = 1;

  requestAnimationFrame(step);
}

function gameOver() {
  running = false;
  msgEl.innerHTML = "Game Over!<br>Score: " + score + "<br><small>Press Space to restart</small>";
  overlay.style.display = "flex";
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

console.log("Asteroids loaded");
init();
// Draw idle state
ctx.fillStyle = "#000"; ctx.fillRect(0, 0, W, H);
ctx.save(); ctx.translate(ship.x, ship.y); ctx.rotate(ship.angle);
ctx.strokeStyle = "#fff"; ctx.lineWidth = 1.5;
ctx.beginPath(); ctx.moveTo(16,0); ctx.lineTo(-10,-10); ctx.lineTo(-6,0); ctx.lineTo(-10,10); ctx.closePath(); ctx.stroke();
ctx.restore();
ctx.strokeStyle = "#888"; ctx.lineWidth = 1.5;
for (var i = 0; i < asteroids.length; i++) {
  var a = asteroids[i];
  ctx.save(); ctx.translate(a.x, a.y); ctx.rotate(a.rot);
  ctx.beginPath();
  for (var j = 0; j < a.radii.length; j++) {
    var ang = (j/a.radii.length)*Math.PI*2;
    if (j===0) ctx.moveTo(Math.cos(ang)*a.radii[j],Math.sin(ang)*a.radii[j]);
    else ctx.lineTo(Math.cos(ang)*a.radii[j],Math.sin(ang)*a.radii[j]);
  }
  ctx.closePath(); ctx.stroke(); ctx.restore();
}