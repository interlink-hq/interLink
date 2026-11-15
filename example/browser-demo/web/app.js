"use strict";

const join = document.getElementById("join");
const stop = document.getElementById("stop");
const state = document.getElementById("state");
const output = document.getElementById("output");
let socket = null;
let worker = null;
let timer = null;
let processed = 0;

function leave(message) {
  clearTimeout(timer);
  if (worker) worker.terminate();
  if (socket) {
    socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
    socket.close();
  }
  worker = socket = null;
  join.disabled = false;
  stop.disabled = true;
  state.textContent = message;
}

join.onclick = () => {
  join.disabled = true;
  stop.disabled = false;
  state.textContent = "Connecting";
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(`${scheme}//${location.host}/ws?consent=yes`);
  worker = new Worker("worker.js");
  worker.onerror = () => leave("Worker error — stopped");
  socket.onopen = () => { state.textContent = "Connected / idle"; };
  socket.onclose = () => leave("Disconnected — join again to resume");
  socket.onerror = () => leave("Connection error — stopped");
  socket.onmessage = (event) => {
    try {
      const task = JSON.parse(event.data);
      if (task.type !== "task" || typeof task.attempt !== "string" ||
          typeof task.text !== "string" || task.text.length > 16639) {
        throw new Error("Invalid task");
      }
      state.textContent = "Working";
      output.textContent = task.text;
      timer = setTimeout(() => leave("Task timed out — stopped"), 9000);
      worker.postMessage(task);
    } catch {
      leave("Invalid task — stopped");
    }
  };
  worker.onmessage = (event) => {
    clearTimeout(timer);
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify(event.data));
    processed++;
    document.getElementById("progress").textContent = `Chunks processed: ${processed}`;
    output.textContent = JSON.stringify(event.data.counts, null, 2);
    state.textContent = "Connected / idle";
  };
};

stop.onclick = () => leave("Not participating");
window.addEventListener("pagehide", () => leave("Not participating"));
