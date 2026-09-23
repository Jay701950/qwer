import express from "express";
import helmet from "helmet";
import dns from "node:dns/promises";
import net from "node:net";
import { Readable, Transform } from "node:stream";
import { Agent } from "undici";

const app = express();
app.disable("x-powered-by");
app.set("trust proxy", 1);
app.use(helmet({ contentSecurityPolicy: false, crossOriginResourcePolicy: false }));
app.use(express.json({ limit: "16kb" }));
app.use(express.static("public", { index: "index.html" }));

const PORT = Number(process.env.PORT || 10000);
const API_KEY = process.env.PROXY_API_KEY || "";
const MAX_BYTES = Number(process.env.MAX_RESPONSE_BYTES || 10 * 1024 * 1024);
const TIMEOUT_MS = Number(process.env.REQUEST_TIMEOUT_MS || 20000);
const MAX_REDIRECTS = Number(process.env.MAX_REDIRECTS || 5);
const WINDOW_MS = Number(process.env.RATE_WINDOW_MS || 60_000);
const MAX_REQUESTS = Number(process.env.RATE_LIMIT || 60);
const ALLOWED_DOMAINS = (process.env.ALLOWED_DOMAINS || "")
  .split(",").map((item) => item.trim().toLowerCase()).filter(Boolean);
const ALLOW_ANY_DOMAIN = process.env.ALLOW_ANY_DOMAIN === "true";
const buckets = new Map();
const dnsCache = new Map();
const responseCache = new Map();
const DNS_CACHE_MS = 5 * 60_000;
const STATIC_CACHE_MS = 2 * 60_000;
const MAX_CACHE_BYTES = 40 * 1024 * 1024;
let cachedBytes = 0;
const upstreamAgent = new Agent({
  connections: 64,
  pipelining: 1,
  keepAliveTimeout: 10_000,
  keepAliveMaxTimeout: 30_000,
  connect: { timeout: TIMEOUT_MS }
});

function isPrivateIpv4(ip) {
  const p = ip.split(".").map(Number);
  if (p.length !== 4 || p.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) return false;
  const [a, b] = p;
  return a === 0 || a === 10 || a === 127 || (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) || (a === 198 && (b === 18 || b === 19)) || a >= 224;
}

function isPrivateIpv6(ip) {
  const normalized = ip.toLowerCase().replace(/^\[|\]$/g, "");
  return normalized === "::" || normalized === "::1" || normalized.startsWith("fe80:") ||
    normalized.startsWith("fc") || normalized.startsWith("fd") || normalized.startsWith("ff");
}

export function isBlockedAddress(hostname) {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (host === "localhost" || host.endsWith(".localhost") || host === "metadata.google.internal") return true;
  if (net.isIPv4(host)) return isPrivateIpv4(host);
  if (net.isIPv6(host)) return isPrivateIpv6(host);
  return false;
}

export function isAllowedHost(hostname) {
  if (ALLOW_ANY_DOMAIN) return true;
  const host = hostname.toLowerCase().replace(/\.$/, "");
  return ALLOWED_DOMAINS.some((domain) => host === domain || host.endsWith(`.${domain}`));
}

export async function validateTarget(rawUrl) {
  let target;
  try { target = new URL(rawUrl); } catch { throw new Error("Invalid URL"); }
  if (!['http:', 'https:'].includes(target.protocol)) throw new Error("Only http and https URLs are supported");
  if (target.username || target.password) throw new Error("Credentials in URLs are not allowed");
  if (isBlockedAddress(target.hostname) || !isAllowedHost(target.hostname)) throw new Error("Target host is not allowed");
  const cacheKey = target.hostname.toLowerCase();
  const cachedDns = dnsCache.get(cacheKey);
  let addresses;
  if (cachedDns && Date.now() - cachedDns.time < DNS_CACHE_MS) {
    addresses = cachedDns.addresses;
  } else {
    addresses = await dns.lookup(target.hostname, { all: true, verbatim: true });
    dnsCache.set(cacheKey, { time: Date.now(), addresses });
  }
  if (!addresses.length || addresses.some(({ address }) => isBlockedAddress(address))) {
    throw new Error("Target resolves to a blocked network address");
  }
  return target;
}

function getCachedResponse(url) {
  const entry = responseCache.get(url);
  if (!entry) return null;
  if (Date.now() - entry.time > STATIC_CACHE_MS) {
    responseCache.delete(url);
    cachedBytes -= entry.body.length;
    return null;
  }
  return entry;
}

function cacheResponse(url, entry) {
  if (entry.body.length > 2 * 1024 * 1024) return;
  while (cachedBytes + entry.body.length > MAX_CACHE_BYTES && responseCache.size) {
    const oldest = responseCache.keys().next().value;
    const removed = responseCache.get(oldest);
    responseCache.delete(oldest);
    cachedBytes -= removed.body.length;
  }
  responseCache.set(url, entry);
  cachedBytes += entry.body.length;
}

function rateLimit(req, res, next) {
  const key = req.ip || "unknown";
  const now = Date.now();
  const current = buckets.get(key);
  if (!current || now - current.startedAt >= WINDOW_MS) {
    buckets.set(key, { startedAt: now, count: 1 });
    return next();
  }
  current.count += 1;
  if (current.count > MAX_REQUESTS) {
    const retryAfter = Math.ceil((WINDOW_MS - (now - current.startedAt)) / 1000);
    res.set("Retry-After", String(retryAfter));
    return res.status(429).json({ error: "Rate limit exceeded", retryAfter });
  }
  next();
}

function requireApiKey(req, res, next) {
  if (!API_KEY) return res.status(503).json({ error: "Proxy is not configured: set PROXY_API_KEY" });
  const supplied = req.get("x-api-key") || (req.get("authorization") || "").replace(/^Bearer\s+/i, "");
  if (supplied !== API_KEY) return res.status(401).json({ error: "Invalid or missing API key" });
  next();
}

function proxyUrl(url) {
  return `/view?url=${encodeURIComponent(url.toString())}`;
}

function rewriteReference(value, baseUrl) {
  const raw = value.trim();
  if (!raw || raw.startsWith("#") || /^(data|blob|javascript|mailto|tel|about):/i.test(raw)) return value;
  try { return proxyUrl(new URL(raw, baseUrl)); } catch { return value; }
}

export function rewriteHtml(html, baseUrl) {
  let output = html;
  // Keep navigation through the proxy, but let static assets use the origin/CDN directly.
  output = output.replace(/(<a\b[^>]*?\bhref\s*=\s*["'])([^"']+)(["'][^>]*>)/gi,
    (_m, prefix, value, suffix) => `${prefix}${rewriteReference(value, baseUrl)}${suffix}`);
  output = output.replace(/(\b(?:src|poster|background|cite)\s*=\s*["'])([^"']+)(["'])/gi,
    (_m, prefix, value, suffix) => `${prefix}${absoluteReference(value, baseUrl)}${suffix}`);
  output = output.replace(/(<link\b[^>]*?\bhref\s*=\s*["'])([^"']+)(["'][^>]*>)/gi,
    (_m, prefix, value, suffix) => `${prefix}${absoluteReference(value, baseUrl)}${suffix}`);
  output = output.replace(/(<form\b[^>]*?\baction\s*=\s*["'])([^"']+)(["'][^>]*>)/gi,
    (_m, prefix, value, suffix) => `${prefix}${rewriteReference(value, baseUrl)}${suffix}`);
  output = output.replace(/(\bsrcset\s*=\s*["'])([^"']+)(["'])/gi, (_m, prefix, value, suffix) => {
    const rewritten = value.split(",").map((part) => {
      const match = part.trim().match(/^(\S+)(\s+.*)?$/);
      return match ? `${absoluteReference(match[1], baseUrl)}${match[2] || ""}` : part;
    }).join(", ");
    return `${prefix}${rewritten}${suffix}`;
  });
  output = output.replace(/url\(\s*(["']?)([^)'"\s]+)\1\s*\)/gi,
    (_m, quote, value) => `url(${quote}${absoluteReference(value, baseUrl)}${quote})`);
  output = output.replace(/(<meta[^>]+http-equiv\s*=\s*["']?refresh["']?[^>]+content\s*=\s*["'][^;]+;\s*url=)([^"']+)/gi,
    (_m, prefix, value) => `${prefix}${rewriteReference(value, baseUrl)}`);
  return output;
}

function absoluteReference(value, baseUrl) {
  const raw = value.trim();
  if (!raw || raw.startsWith("#") || /^(data|blob|javascript|mailto|tel|about):/i.test(raw)) return value;
  try { return new URL(raw, baseUrl).toString(); } catch { return value; }
}

function copySafeHeaders(upstream, res) {
  const contentType = upstream.headers.get("content-type");
  if (contentType) res.set("content-type", contentType);
  const location = upstream.headers.get("location");
  if (location) res.set("location", proxyUrl(location));
  const setCookie = upstream.headers.get("set-cookie");
  if (setCookie) res.set("set-cookie", setCookie.split(/,(?=[^;]+?=)/).map((cookie) => cookie.replace(/;\s*Domain=[^;]+/gi, "").replace(/;\s*Secure/gi, "")).join(","));
}

async function readLimitedBody(response) {
  const length = Number(response.headers.get("content-length") || 0);
  if (length > MAX_BYTES) throw new Error("Upstream response is too large");
  if (!response.body) return Buffer.alloc(0);
  const reader = response.body.getReader();
  const chunks = [];
  let total = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > MAX_BYTES) { await reader.cancel(); throw new Error("Upstream response is too large"); }
    chunks.push(Buffer.from(value));
  }
  return Buffer.concat(chunks);
}

function streamLimitedBody(response, res) {
  let total = 0;
  const limiter = new Transform({
    transform(chunk, _encoding, callback) {
      total += chunk.length;
      if (total > MAX_BYTES) callback(new Error("Upstream response is too large"));
      else callback(null, chunk);
    }
  });
  limiter.on("error", () => { if (!res.headersSent) res.status(502); res.destroy(); });
  Readable.fromWeb(response.body).pipe(limiter).pipe(res);
}

app.get("/health", (_req, res) => res.json({ ok: true, service: "safe-proxy" }));

async function proxyRequest(req, res) {
  const rawUrl = String(req.query.url || "");
  if (!rawUrl) return res.status(400).json({ error: "Missing url query parameter" });
  try {
    let target = await validateTarget(rawUrl);
    let upstream;
    for (let redirect = 0; redirect <= MAX_REDIRECTS; redirect += 1) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), TIMEOUT_MS);
      try {
        upstream = await fetch(target, {
          method: "GET", redirect: "manual", signal: controller.signal,
          dispatcher: upstreamAgent,
          headers: { "user-agent": "Mozilla/5.0 (compatible; RenderSafeProxy/1.0)", accept: req.get("accept") || "*/*", "accept-language": req.get("accept-language") || "en-US,en;q=0.8" }
        });
      } finally { clearTimeout(timeout); }
      if (![301, 302, 303, 307, 308].includes(upstream.status)) break;
      const location = upstream.headers.get("location");
      if (!location || redirect === MAX_REDIRECTS) throw new Error("Too many or invalid redirects");
      target = await validateTarget(new URL(location, target).toString());
    }
    const cached = getCachedResponse(target.href);
    if (cached) {
      return res.status(cached.status)
        .set("content-type", cached.contentType)
        .set("x-proxy-cache", "HIT")
        .set("cache-control", "public, max-age=120")
        .send(cached.body);
    }
    const contentType = upstream.headers.get("content-type") || "application/octet-stream";
    copySafeHeaders(upstream, res);
    const isHtml = /^text\/html(?:;|$)/i.test(contentType);
    const isCss = /^text\/css(?:;|$)/i.test(contentType);
    const isStatic = !isHtml && !isCss && upstream.status === 200;
    if (!isHtml && !isCss && upstream.status === 200 && upstream.body) {
      res.status(upstream.status).set("x-proxy-upstream", target.origin)
        .set("x-proxy-cache", "STREAM").set("cache-control", "public, max-age=120");
      return streamLimitedBody(upstream, res);
    }
    const body = await readLimitedBody(upstream);
    if (isStatic) cacheResponse(target.href, { time: Date.now(), status: upstream.status, contentType, body });
    res.status(upstream.status).set("x-proxy-upstream", target.origin)
      .set("x-proxy-cache", "MISS")
      .set("cache-control", isStatic ? "public, max-age=120" : "no-store");
    if (isHtml) return res.send(rewriteHtml(body.toString("utf8"), target));
    if (/^text\/css(?:;|$)/i.test(contentType)) return res.send(body.toString("utf8").replace(/url\(\s*(["']?)([^)'"\s]+)\1\s*\)/gi, (_m, q, value) => `url(${q}${absoluteReference(value, target)}${q})`));
    return res.send(body);
  } catch (error) {
    const message = error.name === "AbortError" ? "Upstream request timed out" : error.message;
    return res.status(502).json({ error: message || "Proxy request failed" });
  }
}

// Direct/API clients must send the API key. The browser UI uses this same
// handler through /view, so the secret never needs to be embedded in HTML.
app.get("/proxy", rateLimit, requireApiKey, proxyRequest);
app.get("/view", rateLimit, proxyRequest);

app.use((_req, res) => res.status(404).json({ error: "Not found" }));

if (process.env.NODE_ENV !== "test") app.listen(PORT, "0.0.0.0", () => console.log(`safe-proxy listening on ${PORT}`));

export { app };
