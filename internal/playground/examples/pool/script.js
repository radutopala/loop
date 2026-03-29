import planck from "planck";

var canvas = document.getElementById("game");
var ctx = canvas.getContext("2d");
var infoEl = document.getElementById("info");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");

var SCALE = 50; // pixels per meter
var TABLE_W = 12, TABLE_H = 6; // meters
var BALL_R = 0.15;
var POCKET_R = 0.35;
var CW, CH, OX, OY;

function resize() {
  canvas.width = window.innerWidth; canvas.height = window.innerHeight;
  var fitW = canvas.width * 0.85, fitH = canvas.height * 0.8;
  SCALE = Math.min(fitW / TABLE_W, fitH / TABLE_H);
  CW = TABLE_W * SCALE; CH = TABLE_H * SCALE;
  OX = (canvas.width - CW) / 2; OY = (canvas.height - CH) / 2;
}
resize();

var world, cueBall, balls, pocketed, running, dragging, dragStart, dragEnd, settling, settleCount;

var BALL_COLORS = [
  "#ffd700", // 1 yellow
  "#0044cc", // 2 blue
  "#cc0000", // 3 red
  "#4400aa", // 4 purple
  "#ff6600", // 5 orange
  "#006633", // 6 green
  "#880000", // 7 maroon
  "#111111", // 8 black
  "#ffd700", // 9 yellow stripe
  "#0044cc", // 10 blue stripe
  "#cc0000", // 11 red stripe
  "#4400aa", // 12 purple stripe
  "#ff6600", // 13 orange stripe
  "#006633", // 14 green stripe
  "#880000", // 15 maroon stripe
];

var pockets = [
  [0, 0], [TABLE_W/2, 0], [TABLE_W, 0],
  [0, TABLE_H], [TABLE_W/2, TABLE_H], [TABLE_W, TABLE_H]
];

function setup() {
  world = new planck.World({ gravity: planck.Vec2(0, 0) });
  balls = [];
  pocketed = [];
  dragging = false;
  settling = false;
  settleCount = 0;

  // Table walls (cushions)
  var cush = 0.3;
  // Top
  world.createBody({ type: "static", position: planck.Vec2(TABLE_W/2, -cush/2) }).createFixture(planck.Box(TABLE_W/2, cush/2), { restitution: 0.7, friction: 0.1 });
  // Bottom
  world.createBody({ type: "static", position: planck.Vec2(TABLE_W/2, TABLE_H + cush/2) }).createFixture(planck.Box(TABLE_W/2, cush/2), { restitution: 0.7, friction: 0.1 });
  // Left
  world.createBody({ type: "static", position: planck.Vec2(-cush/2, TABLE_H/2) }).createFixture(planck.Box(cush/2, TABLE_H/2), { restitution: 0.7, friction: 0.1 });
  // Right
  world.createBody({ type: "static", position: planck.Vec2(TABLE_W + cush/2, TABLE_H/2) }).createFixture(planck.Box(cush/2, TABLE_H/2), { restitution: 0.7, friction: 0.1 });

  // Cue ball
  cueBall = createBall(TABLE_W * 0.25, TABLE_H / 2, -1);

  // Rack (triangle formation)
  var startX = TABLE_W * 0.72, startY = TABLE_H / 2;
  var order = [0, 1, 8, 2, 3, 4, 7, 5, 6, 9, 10, 11, 12, 13, 14];
  var idx = 0;
  var spacing = BALL_R * 2.1;
  for (var row = 0; row < 5; row++) {
    for (var col = 0; col <= row; col++) {
      var x = startX + row * spacing * 0.866;
      var y = startY + (col - row / 2) * spacing;
      createBall(x, y, order[idx]);
      idx++;
    }
  }
}

function createBall(x, y, num) {
  var body = world.createBody({ type: "dynamic", position: planck.Vec2(x, y), bullet: true, linearDamping: 0.8, angularDamping: 0.6 });
  body.createFixture(planck.Circle(BALL_R), { density: 1.0, restitution: 0.9, friction: 0.05 });
  body.num = num;
  balls.push(body);
  return body;
}

function toScreen(x, y) { return [OX + x * SCALE, OY + y * SCALE]; }

function draw() {
  ctx.fillStyle = "#1a0a00";
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  // Table felt
  ctx.fillStyle = "#0a6e3a";
  ctx.shadowColor = "rgba(0,0,0,0.5)"; ctx.shadowBlur = 20;
  ctx.fillRect(OX, OY, CW, CH);
  ctx.shadowBlur = 0;

  // Rails
  ctx.strokeStyle = "#5c3317";
  ctx.lineWidth = 12;
  ctx.strokeRect(OX - 6, OY - 6, CW + 12, CH + 12);
  ctx.strokeStyle = "#7a4422";
  ctx.lineWidth = 4;
  ctx.strokeRect(OX - 14, OY - 14, CW + 28, CH + 28);

  // Pockets
  for (var i = 0; i < pockets.length; i++) {
    var sp = toScreen(pockets[i][0], pockets[i][1]);
    ctx.fillStyle = "#111";
    ctx.beginPath(); ctx.arc(sp[0], sp[1], POCKET_R * SCALE, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = "#333"; ctx.lineWidth = 2; ctx.stroke();
  }

  // Balls
  for (var i = 0; i < balls.length; i++) {
    var b = balls[i];
    var pos = b.getPosition();
    var sp = toScreen(pos.x, pos.y);
    var r = BALL_R * SCALE;

    if (b.num === -1) {
      // Cue ball
      ctx.fillStyle = "#fff";
      ctx.shadowColor = "rgba(0,0,0,0.4)"; ctx.shadowBlur = 4;
      ctx.beginPath(); ctx.arc(sp[0], sp[1], r, 0, Math.PI*2); ctx.fill();
      ctx.shadowBlur = 0;
      ctx.strokeStyle = "#ddd"; ctx.lineWidth = 1; ctx.stroke();
    } else {
      var color = BALL_COLORS[b.num];
      var isStripe = b.num >= 8;
      ctx.shadowColor = "rgba(0,0,0,0.4)"; ctx.shadowBlur = 4;
      if (isStripe && b.num > 8) {
        // Stripe: white with colored band
        ctx.fillStyle = "#fff";
        ctx.beginPath(); ctx.arc(sp[0], sp[1], r, 0, Math.PI*2); ctx.fill();
        ctx.fillStyle = color;
        ctx.beginPath(); ctx.arc(sp[0], sp[1], r, -0.8, 0.8); ctx.fill();
        ctx.beginPath(); ctx.arc(sp[0], sp[1], r, Math.PI-0.8, Math.PI+0.8); ctx.fill();
      } else {
        ctx.fillStyle = color;
        ctx.beginPath(); ctx.arc(sp[0], sp[1], r, 0, Math.PI*2); ctx.fill();
      }
      ctx.shadowBlur = 0;
      // Number circle
      ctx.fillStyle = "#fff";
      ctx.beginPath(); ctx.arc(sp[0], sp[1], r * 0.4, 0, Math.PI*2); ctx.fill();
      ctx.fillStyle = "#111";
      ctx.font = "bold " + Math.floor(r * 0.55) + "px sans-serif";
      ctx.textAlign = "center"; ctx.textBaseline = "middle";
      ctx.fillText(b.num + 1, sp[0], sp[1] + 1);
      // Outline
      ctx.strokeStyle = "rgba(0,0,0,0.3)"; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.arc(sp[0], sp[1], r, 0, Math.PI*2); ctx.stroke();
    }
  }

  // Cue stick when aiming
  if (dragging && dragStart && dragEnd) {
    var cuePos = cueBall.getPosition();
    var sp = toScreen(cuePos.x, cuePos.y);
    var dx = dragEnd[0] - dragStart[0];
    var dy = dragEnd[1] - dragStart[1];
    var dist = Math.sqrt(dx*dx + dy*dy);
    if (dist > 5) {
      var angle = Math.atan2(dy, dx);
      // Aiming line (opposite direction)
      ctx.strokeStyle = "rgba(255,255,255,0.3)";
      ctx.lineWidth = 1;
      ctx.setLineDash([4, 4]);
      ctx.beginPath();
      ctx.moveTo(sp[0], sp[1]);
      ctx.lineTo(sp[0] - Math.cos(angle) * 300, sp[1] - Math.sin(angle) * 300);
      ctx.stroke();
      ctx.setLineDash([]);
      // Cue stick
      var stickDist = BALL_R * SCALE + 10 + Math.min(dist * 0.5, 60);
      ctx.strokeStyle = "#c4903d";
      ctx.lineWidth = 6;
      ctx.beginPath();
      ctx.moveTo(sp[0] + Math.cos(angle) * stickDist, sp[1] + Math.sin(angle) * stickDist);
      ctx.lineTo(sp[0] + Math.cos(angle) * (stickDist + 180), sp[1] + Math.sin(angle) * (stickDist + 180));
      ctx.stroke();
      ctx.strokeStyle = "#e8c872";
      ctx.lineWidth = 3;
      ctx.beginPath();
      ctx.moveTo(sp[0] + Math.cos(angle) * stickDist, sp[1] + Math.sin(angle) * stickDist);
      ctx.lineTo(sp[0] + Math.cos(angle) * (stickDist + 20), sp[1] + Math.sin(angle) * (stickDist + 20));
      ctx.stroke();
      // Power indicator
      var power = Math.min(dist / 200, 1);
      ctx.fillStyle = "rgba(255," + Math.floor(255 * (1-power)) + ",0,0.8)";
      ctx.fillRect(OX + CW + 20, OY + CH * (1 - power), 12, CH * power);
      ctx.strokeStyle = "#555"; ctx.lineWidth = 1;
      ctx.strokeRect(OX + CW + 20, OY, 12, CH);
    }
  }

  // Pocketed balls display
  if (pocketed.length > 0) {
    var py = OY + CH + 30;
    for (var i = 0; i < pocketed.length; i++) {
      var num = pocketed[i];
      var color = num === -1 ? "#fff" : BALL_COLORS[num];
      ctx.fillStyle = color;
      ctx.beginPath(); ctx.arc(OX + 15 + i * 22, py, 8, 0, Math.PI*2); ctx.fill();
      ctx.fillStyle = "#fff"; ctx.font = "bold 8px sans-serif"; ctx.textAlign = "center"; ctx.textBaseline = "middle";
      if (num >= 0) ctx.fillText(num + 1, OX + 15 + i * 22, py);
    }
  }
}

function checkPockets() {
  for (var i = balls.length - 1; i >= 0; i--) {
    var pos = balls[i].getPosition();
    for (var j = 0; j < pockets.length; j++) {
      var dx = pos.x - pockets[j][0], dy = pos.y - pockets[j][1];
      if (Math.sqrt(dx*dx + dy*dy) < POCKET_R) {
        if (balls[i] === cueBall) {
          // Cue ball pocketed — reset position
          cueBall.setPosition(planck.Vec2(TABLE_W * 0.25, TABLE_H / 2));
          cueBall.setLinearVelocity(planck.Vec2(0, 0));
          cueBall.setAngularVelocity(0);
          infoEl.textContent = "Scratch! Cue ball reset.";
        } else {
          pocketed.push(balls[i].num);
          world.destroyBody(balls[i]);
          balls.splice(i, 1);
          if (balls.length === 1) {
            running = false;
            msgEl.innerHTML = "All balls pocketed!<br><small>Click to play again</small>";
            overlay.style.display = "flex";
          }
        }
        break;
      }
    }
  }
}

function allSettled() {
  for (var i = 0; i < balls.length; i++) {
    var v = balls[i].getLinearVelocity();
    if (v.length() > 0.05) return false;
  }
  return true;
}

function gameStep() {
  if (!running) return;
  world.step(1/60);
  checkPockets();

  if (settling) {
    if (allSettled()) {
      settleCount++;
      if (settleCount > 10) {
        settling = false;
        infoEl.textContent = "Your shot";
        // Stop all residual movement
        for (var i = 0; i < balls.length; i++) {
          balls[i].setLinearVelocity(planck.Vec2(0, 0));
          balls[i].setAngularVelocity(0);
        }
      }
    } else {
      settleCount = 0;
    }
  }

  draw();
  requestAnimationFrame(gameStep);
}

// Input
canvas.addEventListener("mousedown", function(e) {
  if (!running) { start(); return; }
  if (settling) return;
  var mx = e.clientX, my = e.clientY;
  var cuePos = cueBall.getPosition();
  var sp = toScreen(cuePos.x, cuePos.y);
  var dx = mx - sp[0], dy = my - sp[1];
  if (Math.sqrt(dx*dx + dy*dy) < BALL_R * SCALE + 20) {
    dragging = true;
    dragStart = [mx, my];
    dragEnd = [mx, my];
  }
});

canvas.addEventListener("mousemove", function(e) {
  if (dragging) dragEnd = [e.clientX, e.clientY];
});

canvas.addEventListener("mouseup", function() {
  if (dragging && dragStart && dragEnd) {
    var dx = dragEnd[0] - dragStart[0];
    var dy = dragEnd[1] - dragStart[1];
    var dist = Math.sqrt(dx*dx + dy*dy);
    if (dist > 10) {
      var power = Math.min(dist / 200, 1) * 25;
      var angle = Math.atan2(dy, dx);
      cueBall.setLinearVelocity(planck.Vec2(-Math.cos(angle) * power, -Math.sin(angle) * power));
      settling = true;
      settleCount = 0;
      infoEl.textContent = "...";
    }
    dragging = false;
    dragStart = null;
    dragEnd = null;
  }
});

function start() {
  setup();
  overlay.style.display = "none";
  running = true;
  requestAnimationFrame(gameStep);
}

window.addEventListener("resize", function() { resize(); });

console.log("Pool loaded");
setup(); draw();