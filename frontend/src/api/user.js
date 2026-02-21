import { GO_API_BASE } from "./paidApi.js";

async function handleJSON(resp) {
  const text = await resp.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch (_) {
    data = { message: text };
  }
  if (!resp.ok) {
    throw new Error(data?.message || resp.statusText || "请求失败");
  }
  return data;
}

export async function getBackendProfile() {
  const resp = await fetch(`${GO_API_BASE}/profile`, { credentials: "include" });
  return handleJSON(resp);
}


