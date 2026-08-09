/**
 * Gofun IT 公共 HTTP 小工具。Authorization 头直接传 JWT（无 Bearer）。
 */
export function baseURL() {
  return process.env.BASE_URL || "http://127.0.0.1:18080/api/v1";
}

export async function http(method, path, { token, body, headers = {} } = {}) {
  const res = await fetch(`${baseURL()}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: token } : {}),
      ...headers,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { raw: text };
    }
  }
  return { status: res.status, data, ok: res.status >= 200 && res.status < 300 };
}

export async function registerAndLogin(username, password = "12345678") {
  let lastErr = null;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const reg = await http("POST", "/register", {
      body: { username, password },
    });
    // 已存在 / 成功都继续登录；429 则退避重试
    if (reg.status === 429) {
      lastErr = new Error(`register rate-limited: ${JSON.stringify(reg.data)}`);
      await new Promise((r) => setTimeout(r, 200 * (attempt + 1)));
      continue;
    }
    const login = await http("POST", "/login", { body: { username, password } });
    if (login.status === 429) {
      lastErr = new Error(`login rate-limited: ${JSON.stringify(login.data)}`);
      await new Promise((r) => setTimeout(r, 200 * (attempt + 1)));
      continue;
    }
    const token = login.data?.data?.access_token || login.data?.data?.token;
    if (!token) {
      lastErr = new Error(`login failed for ${username}: ${JSON.stringify(login.data)}`);
      await new Promise((r) => setTimeout(r, 150 * (attempt + 1)));
      continue;
    }
    const me = await http("GET", "/user/info", { token });
    const userID = String(me.data?.data?.id || "");
    if (!userID) {
      throw new Error(`user info missing id: ${JSON.stringify(me.data)}`);
    }
    return { token, userID, username };
  }
  throw lastErr || new Error(`registerAndLogin exhausted for ${username}`);
}

export async function loginAdmin() {
  const username = process.env.ADMIN_USERNAME || "admin";
  const password = process.env.ADMIN_PASSWORD || "admin123";
  const login = await http("POST", "/login", { body: { username, password } });
  const token = login.data?.data?.access_token || login.data?.data?.token;
  if (!token) {
    throw new Error(`admin login failed: ${JSON.stringify(login.data)}`);
  }
  return token;
}

export function assert(cond, message) {
  if (!cond) throw new Error(message);
}

export function purchaseBody(name = "并发测试") {
  return {
    quantity: 1,
    contact_name: name,
    contact_phone: "13800138000",
    terms_accepted: true,
    attendees: [],
  };
}
