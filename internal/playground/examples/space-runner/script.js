import * as THREE from "https://esm.sh/three@0.170.0";

var W = window.innerWidth, H = window.innerHeight;
var scene = new THREE.Scene();
scene.fog = new THREE.FogExp2(0x000011, 0.015);
var camera = new THREE.PerspectiveCamera(70, W / H, 0.1, 500);
var renderer = new THREE.WebGLRenderer({ canvas: document.getElementById("game"), antialias: true });
renderer.setSize(W, H);
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));

var scoreEl = document.getElementById("score");
var speedEl = document.getElementById("speed");
var overlay = document.getElementById("overlay");
var msgEl = document.getElementById("msg");

var keys = {};
var shipX = 0, shipY = 0, shipRoll = 0, shipPitch = 0;
var score = 0, running = false, dead = false, animId;
var bullets = [], obstacles = [], particles = [], rings = [];
var clock = new THREE.Clock();
var gameSpeed = 1, distance = 0;
var TUNNEL_LEN = 300, SPAWN_DIST = 250;

scene.add(new THREE.AmbientLight(0x334466, 0.8));
var headlight = new THREE.PointLight(0x00ff88, 2, 80);
scene.add(headlight);
var sunLight = new THREE.DirectionalLight(0x6688ff, 0.6);
sunLight.position.set(1, 2, -1);
scene.add(sunLight);

var shipGroup = new THREE.Group();
var fuselageGeo = new THREE.ConeGeometry(0.3, 1.6, 6);
fuselageGeo.rotateX(Math.PI / 2);
shipGroup.add(new THREE.Mesh(fuselageGeo, new THREE.MeshPhongMaterial({ color: 0x2288ff, emissive: 0x112244, shininess: 60, flatShading: true })));
var wingMesh = new THREE.Mesh(new THREE.BoxGeometry(2.4, 0.06, 0.6), new THREE.MeshPhongMaterial({ color: 0x1166cc, emissive: 0x0a1133, flatShading: true }));
wingMesh.position.z = 0.2;
shipGroup.add(wingMesh);
var engineGeo = new THREE.SphereGeometry(0.15, 8, 8);
var engineMat = new THREE.MeshBasicMaterial({ color: 0x00ff66 });
var engineL = new THREE.Mesh(engineGeo, engineMat);
engineL.position.set(-0.5, -0.05, 0.7);
shipGroup.add(engineL);
var engineR = new THREE.Mesh(engineGeo, engineMat);
engineR.position.set(0.5, -0.05, 0.7);
shipGroup.add(engineR);
var cockpitMesh = new THREE.Mesh(new THREE.SphereGeometry(0.18, 8, 6), new THREE.MeshPhongMaterial({ color: 0x66ffff, emissive: 0x225555, transparent: true, opacity: 0.7 }));
cockpitMesh.position.set(0, 0.12, -0.2);
cockpitMesh.scale.set(1, 0.8, 1.2);
shipGroup.add(cockpitMesh);
shipGroup.position.z = -5;
scene.add(shipGroup);

camera.position.set(0, 1.5, 0);
camera.lookAt(0, 0, -20);

var starCount = 3000;
var starPos = new Float32Array(starCount * 3);
for (var i = 0; i < starCount; i++) {
  starPos[i*3] = (Math.random() - 0.5) * 200;
  starPos[i*3+1] = (Math.random() - 0.5) * 200;
  starPos[i*3+2] = -Math.random() * TUNNEL_LEN;
}
var starGeo = new THREE.BufferGeometry();
starGeo.setAttribute("position", new THREE.BufferAttribute(starPos, 3));
var starField = new THREE.Points(starGeo, new THREE.PointsMaterial({ color: 0xffffff, size: 0.15, sizeAttenuation: true }));
scene.add(starField);

var ringMat = new THREE.MeshBasicMaterial({ color: 0x003322, wireframe: true, transparent: true, opacity: 0.15 });
for (var z = -20; z > -TUNNEL_LEN; z -= 15) {
  var ring = new THREE.Mesh(new THREE.TorusGeometry(12, 0.1, 6, 24), ringMat);
  ring.position.z = z; ring.rotation.x = Math.random() * 0.3;
  scene.add(ring); rings.push(ring);
}

var obstacleMats = [
  new THREE.MeshPhongMaterial({ color: 0xff2244, emissive: 0x440011, flatShading: true }),
  new THREE.MeshPhongMaterial({ color: 0xff8800, emissive: 0x441100, flatShading: true }),
  new THREE.MeshPhongMaterial({ color: 0xaa00ff, emissive: 0x220044, flatShading: true }),
];
var obstacleGeos = [new THREE.OctahedronGeometry(1,0), new THREE.TetrahedronGeometry(1.2,0), new THREE.IcosahedronGeometry(0.9,0)];

function spawnObstacle() {
  var type = Math.floor(Math.random() * 3);
  var mesh = new THREE.Mesh(obstacleGeos[type], obstacleMats[type]);
  mesh.position.set((Math.random()-0.5)*14, (Math.random()-0.5)*8, shipGroup.position.z - SPAWN_DIST - Math.random()*40);
  var s = 0.6 + Math.random() * 1.2;
  mesh.scale.setScalar(s);
  mesh.userData = { radius: s, rotSpeed: new THREE.Vector3(Math.random()*2-1, Math.random()*2-1, Math.random()*2-1) };
  scene.add(mesh); obstacles.push(mesh);
}

var bulletGeo = new THREE.CylinderGeometry(0.05, 0.05, 1.2, 6);
bulletGeo.rotateX(Math.PI / 2);
var bulletMat = new THREE.MeshBasicMaterial({ color: 0x00ff44 });
var shootCooldown = 0;

function shoot() {
  var b = new THREE.Mesh(bulletGeo, bulletMat);
  b.position.copy(shipGroup.position); b.position.z -= 1;
  b.userData = { life: 120 };
  scene.add(b); bullets.push(b);
}

var particleGeo = new THREE.SphereGeometry(0.08, 4, 4);
function explode(pos, color) {
  for (var i = 0; i < 15; i++) {
    var mat = new THREE.MeshBasicMaterial({ color: color, transparent: true, opacity: 1 });
    var p = new THREE.Mesh(particleGeo, mat);
    p.position.copy(pos);
    p.userData = { vel: new THREE.Vector3((Math.random()-0.5)*0.4,(Math.random()-0.5)*0.4,(Math.random()-0.5)*0.4), life: 40+Math.random()*30 };
    scene.add(p); particles.push(p);
  }
}

function updateHUD() {
  scoreEl.textContent = "Score: " + score;
  speedEl.textContent = "Speed: " + gameSpeed.toFixed(1) + "x";
}

function gameStep() {
  var dt = Math.min(clock.getDelta(), 0.05);
  var spd = gameSpeed;
  var moveSpeed = 12 * dt;
  var targetRoll = 0, targetPitch = 0;
  if (keys["ArrowLeft"]||keys["a"]) { shipX -= moveSpeed; targetRoll = 0.5; }
  if (keys["ArrowRight"]||keys["d"]) { shipX += moveSpeed; targetRoll = -0.5; }
  if (keys["ArrowUp"]||keys["w"]) { shipY += moveSpeed*0.7; targetPitch = -0.3; }
  if (keys["ArrowDown"]||keys["s"]) { shipY -= moveSpeed*0.7; targetPitch = 0.3; }
  shipX = Math.max(-8, Math.min(8, shipX));
  shipY = Math.max(-5, Math.min(5, shipY));
  shipRoll += (targetRoll - shipRoll) * 5 * dt;
  shipPitch += (targetPitch - shipPitch) * 5 * dt;

  if (shootCooldown > 0) shootCooldown--;
  if (keys[" "] && shootCooldown === 0) { shoot(); shootCooldown = 8; }

  var fwd = 20 * spd * dt;
  shipGroup.position.set(shipX, shipY, shipGroup.position.z - fwd);
  shipGroup.rotation.z = shipRoll; shipGroup.rotation.x = shipPitch;
  distance += fwd;
  gameSpeed = 1 + distance / 500;

  camera.position.x += (shipX*0.3 - camera.position.x) * 3 * dt;
  camera.position.y += ((shipY*0.3+1.5) - camera.position.y) * 3 * dt;
  camera.position.z = shipGroup.position.z + 5;
  camera.lookAt(shipGroup.position.x*0.5, shipGroup.position.y*0.3, shipGroup.position.z - 30);
  headlight.position.copy(shipGroup.position); headlight.position.z -= 3;

  var pulse = 0.8 + 0.4 * Math.sin(Date.now() * 0.01);
  engineL.scale.setScalar(pulse); engineR.scale.setScalar(pulse);

  if (Math.random() < 0.06 * spd) spawnObstacle();

  for (var i = obstacles.length-1; i >= 0; i--) {
    var o = obstacles[i];
    o.rotation.x += o.userData.rotSpeed.x * dt;
    o.rotation.y += o.userData.rotSpeed.y * dt;
    if (o.position.z > shipGroup.position.z + 20) { scene.remove(o); obstacles.splice(i,1); continue; }
    var dx = shipGroup.position.x-o.position.x, dy = shipGroup.position.y-o.position.y, dz = shipGroup.position.z-o.position.z;
    if (Math.sqrt(dx*dx+dy*dy+dz*dz) < o.userData.radius + 0.4) {
      explode(shipGroup.position, 0x2288ff);
      running = false; dead = true;
      msgEl.innerHTML = "Destroyed!<br>Score: "+score+"<br>Distance: "+Math.floor(distance)+"m<br><small>Press Space to restart</small>";
      overlay.style.display = "flex"; return;
    }
  }

  for (var i = bullets.length-1; i >= 0; i--) {
    var b = bullets[i];
    b.position.z -= 80 * dt; b.userData.life--;
    if (b.userData.life <= 0) { scene.remove(b); bullets.splice(i,1); continue; }
    for (var j = obstacles.length-1; j >= 0; j--) {
      var o = obstacles[j];
      var bx=b.position.x-o.position.x, by=b.position.y-o.position.y, bz=b.position.z-o.position.z;
      if (Math.sqrt(bx*bx+by*by+bz*bz) < o.userData.radius+0.2) {
        explode(o.position, 0xff4400);
        scene.remove(o); obstacles.splice(j,1);
        scene.remove(b); bullets.splice(i,1);
        score += Math.floor(10*spd); updateHUD(); break;
      }
    }
  }

  for (var i = particles.length-1; i >= 0; i--) {
    var p = particles[i];
    p.position.add(p.userData.vel); p.userData.life--;
    p.material.opacity = p.userData.life / 70;
    if (p.userData.life <= 0) { scene.remove(p); particles.splice(i,1); }
  }

  for (var i = 0; i < rings.length; i++) {
    if (rings[i].position.z > shipGroup.position.z+20) { rings[i].position.z -= TUNNEL_LEN; rings[i].rotation.y = Math.random()*Math.PI; }
  }

  var positions = starField.geometry.attributes.position.array;
  for (var i = 0; i < starCount; i++) {
    if (positions[i*3+2] > shipGroup.position.z+30) {
      positions[i*3+2] -= TUNNEL_LEN;
      positions[i*3] = (Math.random()-0.5)*200;
      positions[i*3+1] = (Math.random()-0.5)*200;
    }
  }
  starField.geometry.attributes.position.needsUpdate = true;

  updateHUD();
  renderer.render(scene, camera);
  animId = requestAnimationFrame(gameStep);
}

function start() {
  for (var i=0;i<obstacles.length;i++) scene.remove(obstacles[i]);
  for (var i=0;i<bullets.length;i++) scene.remove(bullets[i]);
  for (var i=0;i<particles.length;i++) scene.remove(particles[i]);
  obstacles=[]; bullets=[]; particles=[];
  shipX=0; shipY=0; shipRoll=0; shipPitch=0;
  shipGroup.position.set(0,0,-5); shipGroup.rotation.set(0,0,0);
  score=0; distance=0; gameSpeed=1; shootCooldown=0;
  dead=false; running=true; clock.getDelta();
  overlay.style.display="none"; updateHUD();
  animId = requestAnimationFrame(gameStep);
}

function idleRender() {
  if (running) return;
  shipGroup.rotation.z = Math.sin(Date.now()*0.001)*0.1;
  var pulse = 0.8+0.4*Math.sin(Date.now()*0.01);
  engineL.scale.setScalar(pulse); engineR.scale.setScalar(pulse);
  renderer.render(scene, camera);
  requestAnimationFrame(idleRender);
}
idleRender();

document.addEventListener("keydown", function(e) {
  keys[e.key] = true;
  if (["ArrowUp","ArrowDown","ArrowLeft","ArrowRight"," "].indexOf(e.key)>=0) e.preventDefault();
  if (!running && e.key===" ") start();
});
document.addEventListener("keyup", function(e) { keys[e.key]=false; });

window.addEventListener("resize", function() {
  W=window.innerWidth; H=window.innerHeight;
  camera.aspect=W/H; camera.updateProjectionMatrix();
  renderer.setSize(W,H);
});

console.log("Space Runner loaded");