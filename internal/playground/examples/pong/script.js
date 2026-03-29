var canvas = document.getElementById("game");
var ctx = canvas.getContext("2d");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");
var W = canvas.width, H = canvas.height;

var PAD_W = 10, PAD_H = 70, BALL_R = 6, PAD_SPEED = 5, WIN_SCORE = 7;
var p1, p2, ball, score1, score2, running, cpuActive, p2Touched, keys = {};

function init() {
  p1 = { y: H/2 - PAD_H/2 }; p2 = { y: H/2 - PAD_H/2 };
  score1 = 0; score2 = 0; cpuActive = true; p2Touched = false;
  resetBall(1);
}

function resetBall(dir) {
  ball = { x: W/2, y: H/2, vx: 4 * dir, vy: (Math.random()-0.5) * 4 };
}

function draw() {
  ctx.fillStyle = "#111"; ctx.fillRect(0, 0, W, H);
  // Center line
  ctx.setLineDash([6, 8]); ctx.strokeStyle = "#333"; ctx.lineWidth = 2;
  ctx.beginPath(); ctx.moveTo(W/2, 0); ctx.lineTo(W/2, H); ctx.stroke();
  ctx.setLineDash([]);
  // Paddles
  ctx.shadowColor = "#0af"; ctx.shadowBlur = 12;
  ctx.fillStyle = "#0af";
  ctx.fillRect(16, p1.y, PAD_W, PAD_H);
  ctx.shadowColor = "#f44"; ctx.fillStyle = "#f44";
  ctx.fillRect(W - 16 - PAD_W, p2.y, PAD_W, PAD_H);
  ctx.shadowBlur = 0;
  // Ball
  ctx.fillStyle = "#fff"; ctx.shadowColor = "#fff"; ctx.shadowBlur = 10;
  ctx.beginPath(); ctx.arc(ball.x, ball.y, BALL_R, 0, Math.PI*2); ctx.fill();
  ctx.shadowBlur = 0;
  // Score
  ctx.fillStyle = "#555"; ctx.font = "bold 48px monospace"; ctx.textAlign = "center";
  ctx.fillText(score1, W/2 - 60, 55);
  ctx.fillText(score2, W/2 + 60, 55);
}

function step() {
  if (!running) return;
  // P1 controls
  if (keys["w"] || keys["W"]) p1.y = Math.max(0, p1.y - PAD_SPEED);
  if (keys["s"] || keys["S"]) p1.y = Math.min(H - PAD_H, p1.y + PAD_SPEED);
  // P2 controls or CPU
  if (keys["ArrowUp"]) { p2.y = Math.max(0, p2.y - PAD_SPEED); p2Touched = true; cpuActive = false; }
  if (keys["ArrowDown"]) { p2.y = Math.min(H - PAD_H, p2.y + PAD_SPEED); p2Touched = true; cpuActive = false; }
  if (cpuActive) {
    var target = ball.y - PAD_H/2;
    var diff = target - p2.y;
    p2.y += Math.sign(diff) * Math.min(Math.abs(diff), PAD_SPEED * 0.75);
    p2.y = Math.max(0, Math.min(H - PAD_H, p2.y));
  }
  // Ball movement
  ball.x += ball.vx; ball.y += ball.vy;
  // Top/bottom bounce
  if (ball.y - BALL_R < 0) { ball.y = BALL_R; ball.vy = Math.abs(ball.vy); }
  if (ball.y + BALL_R > H) { ball.y = H - BALL_R; ball.vy = -Math.abs(ball.vy); }
  // P1 paddle hit
  if (ball.x - BALL_R < 26 + PAD_W && ball.x - BALL_R > 16 && ball.y > p1.y - BALL_R && ball.y < p1.y + PAD_H + BALL_R) {
    ball.x = 26 + PAD_W + BALL_R;
    var hit = (ball.y - p1.y - PAD_H/2) / (PAD_H/2);
    ball.vx = Math.abs(ball.vx) + 0.2;
    ball.vy = hit * 5;
  }
  // P2 paddle hit
  if (ball.x + BALL_R > W - 26 - PAD_W && ball.x + BALL_R < W - 16 && ball.y > p2.y - BALL_R && ball.y < p2.y + PAD_H + BALL_R) {
    ball.x = W - 26 - PAD_W - BALL_R;
    var hit = (ball.y - p2.y - PAD_H/2) / (PAD_H/2);
    ball.vx = -(Math.abs(ball.vx) + 0.2);
    ball.vy = hit * 5;
  }
  // Speed cap
  var spd = Math.sqrt(ball.vx*ball.vx + ball.vy*ball.vy);
  if (spd > 12) { ball.vx *= 12/spd; ball.vy *= 12/spd; }
  // Scoring
  if (ball.x < -20) { score2++; if (score2 >= WIN_SCORE) { endGame("Player 2 wins!"); return; } resetBall(1); }
  if (ball.x > W + 20) { score1++; if (score1 >= WIN_SCORE) { endGame("Player 1 wins!"); return; } resetBall(-1); }
  draw();
  requestAnimationFrame(step);
}

function endGame(text) {
  running = false;
  msgEl.innerHTML = text + "<br>Score: " + score1 + " - " + score2 + "<br><small>Press Space to restart</small>";
  overlay.style.display = "flex";
}

function start() { init(); overlay.style.display = "none"; running = true; requestAnimationFrame(step); }

document.addEventListener("keydown", function(e) {
  keys[e.key] = true;
  if (["ArrowUp","ArrowDown"," "].indexOf(e.key) >= 0) e.preventDefault();
  if (!running && e.key === " ") start();
});
document.addEventListener("keyup", function(e) { keys[e.key] = false; });

console.log("Pong loaded");
init(); draw();