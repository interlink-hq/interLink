"use strict";

// WASM seam: replace this function with a fixed, trusted module that accepts
// UTF-8 text and returns the same word -> positive integer count object.
// Keep loading/instantiation inside this worker, not on the UI thread.
async function processText(text) {
  const counts = Object.create(null);
  for (const word of text.toLowerCase().match(/[a-z0-9]+/g) || []) {
    counts[word] = (counts[word] || 0) + 1;
  }
  return counts;
}

self.onmessage = async ({ data }) => {
  if (data.type !== "task" || typeof data.text !== "string" ||
      data.text.length > 16639 || typeof data.attempt !== "string") {
    throw new Error("Invalid task");
  }
  const counts = await processText(data.text);
  self.postMessage({ type: "result", attempt: data.attempt, counts });
};
