//k6_compare.js
import http from "k6/http";
import { check, sleep } from "k6";
//
// Usage:
//   k6 run -e BASE_URL=https://api.guangyang.online -e FILE1=/path/a.xlsx -e FILE2=/path/b.xlsx loadtest/k6_compare.js
//
// Notes:
// - For meaningful load tests, temporarily set COMPARE_JOB_FEE_FEN=0 (free) to bypass WeChat payment.
// - This script uploads two files, polls job status, and downloads export when ready.

export const options = {
  vus: 5,
  duration: "1m",
};

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const FILE1 = __ENV.FILE1;
const FILE2 = __ENV.FILE2;

// k6 规则：open() 只能在 init stage（全局作用域）调用
let file1Bytes = null;
let file2Bytes = null;
if (FILE1 && FILE2) {
  file1Bytes = open(FILE1, "b");
  file2Bytes = open(FILE2, "b");
}

function createJob(file1Bytes, file2Bytes) {
  const form = {
    file1: http.file(file1Bytes, "file1.xlsx"),
    file2: http.file(file2Bytes, "file2.xlsx"),
  };
  const res = http.post(`${BASE_URL}/compare/jobs`, form, { timeout: "300s" });
  check(res, { "create job 200": function (r) { return r.status === 200; } });
  return res.json();
}

function getJob(jobId) {
  const res = http.get(`${BASE_URL}/compare/jobs/${encodeURIComponent(jobId)}`, { timeout: "30s" });
  check(res, { "get job 200": function (r) { return r.status === 200; } });
  return res.json();
}

function downloadExport(jobId) {
  // Ask backend for signed URL JSON, then download directly.
  const meta = http.get(`${BASE_URL}/compare/jobs/${encodeURIComponent(jobId)}/export?format=json`, {
    headers: { Accept: "application/json" },
    timeout: "30s",
  });
  check(meta, { "export meta 200": function (r) { return r.status === 200; } });
  const u = (meta.json("url") || "").trim();
  if (!u) return false;
  const res = http.get(u, { timeout: "300s" });
  check(res, { "export download ok": function (r) { return r.status === 200; } });
  return true;
}

export default function () {
  if (!FILE1 || !FILE2) {
    // No files configured: just hit health/metrics.
    http.get(`${BASE_URL}/healthz`);
    sleep(1);
    return;
  }

  const f1 = file1Bytes;
  const f2 = file2Bytes;
  if (!f1 || !f2) {
    sleep(1);
    return;
  }

  const job = createJob(f1, f2);
  const jobId = job && job.jobId;
  if (!jobId) {
    sleep(1);
    return;
  }

  // Poll for up to 90s.
  const deadline = Date.now() + 90000;
  while (Date.now() < deadline) {
    const j = getJob(jobId);
    const st = j && j.status;
    if (st === "ready") {
      downloadExport(jobId);
      break;
    }
    if (st === "failed" || st === "cancelled" || st === "awaiting_payment") {
      break;
    }
    sleep(1.5);
  }
}

