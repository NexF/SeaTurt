/**
 * mcp-server-browser daemon
 *
 * Long-running Node.js process inside the Docker container.
 * Listens on a Unix socket, manages Playwright browser lifecycle,
 * and proxies tool calls to @playwright/mcp.
 *
 * Launched by s6-overlay as a service (svc-browser-daemon).
 */

import net from "net";
import fs from "fs";
import { spawn } from "child_process";

const SOCKET_PATH = "/tmp/mcp-browser.sock";
const USER_DATA_DIR = process.env.BROWSER_USER_DATA_DIR || "/workspace/.seaturt/mcp-servers/browser/user-data";

// Browser state machine: "closed" | "open"
let browserState = "closed";

// The @playwright/mcp subprocess (stdio mode)
let mcpProcess = null;
let mcpStdoutBuffer = "";
let pendingRequests = new Map(); // id -> { resolve, reject }
let nextId = 1;

/**
 * Start the @playwright/mcp subprocess in stdio mode.
 * This spawns `npx @playwright/mcp --headless=false --user-data-dir=...`
 */
function startMCPProcess() {
  return new Promise((resolve, reject) => {
    const args = [
      "@playwright/mcp@latest",
      "--browser=chromium",
      `--user-data-dir=${USER_DATA_DIR}`,
      "--caps=vision,pdf",
    ];

    mcpProcess = spawn("npx", args, {
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, DISPLAY: process.env.DISPLAY || ":1" },
    });

    mcpStdoutBuffer = "";
    pendingRequests.clear();

    mcpProcess.stdout.on("data", (chunk) => {
      mcpStdoutBuffer += chunk.toString();
      processStdoutBuffer();
    });

    mcpProcess.stderr.on("data", (chunk) => {
      // Log playwright-mcp stderr for debugging
      process.stderr.write(`[playwright-mcp] ${chunk}`);
    });

    mcpProcess.on("error", (err) => {
      console.error("[browser-daemon] MCP process error:", err.message);
      handleMCPProcessExit();
      reject(err);
    });

    mcpProcess.on("exit", (code, signal) => {
      console.error(
        `[browser-daemon] MCP process exited: code=${code} signal=${signal}`
      );
      handleMCPProcessExit();
    });

    // Perform MCP initialize handshake
    const initId = nextId++;
    const initReq = {
      jsonrpc: "2.0",
      id: initId,
      method: "initialize",
      params: {
        protocolVersion: "2024-11-05",
        capabilities: {},
        clientInfo: { name: "browser-daemon", version: "1.0.0" },
      },
    };

    pendingRequests.set(initId, {
      resolve: (result) => {
        // Send initialized notification
        const notif = {
          jsonrpc: "2.0",
          method: "notifications/initialized",
        };
        mcpProcess.stdin.write(JSON.stringify(notif) + "\n");
        resolve(result);
      },
      reject,
    });

    mcpProcess.stdin.write(JSON.stringify(initReq) + "\n");
  });
}

function processStdoutBuffer() {
  let newlineIdx;
  while ((newlineIdx = mcpStdoutBuffer.indexOf("\n")) !== -1) {
    const line = mcpStdoutBuffer.slice(0, newlineIdx).trim();
    mcpStdoutBuffer = mcpStdoutBuffer.slice(newlineIdx + 1);
    if (!line) continue;

    try {
      const msg = JSON.parse(line);
      if (msg.id !== undefined && pendingRequests.has(msg.id)) {
        const { resolve, reject } = pendingRequests.get(msg.id);
        pendingRequests.delete(msg.id);
        if (msg.error) {
          reject(new Error(msg.error.message));
        } else {
          resolve(msg.result);
        }
      }
    } catch (e) {
      console.error("[browser-daemon] Failed to parse MCP response:", line);
    }
  }
}

function handleMCPProcessExit() {
  browserState = "closed";
  mcpProcess = null;
  mcpStdoutBuffer = "";
  // Reject all pending requests
  for (const [, { reject }] of pendingRequests) {
    reject(new Error("MCP process exited"));
  }
  pendingRequests.clear();
}

function sendToMCP(method, params) {
  return new Promise((resolve, reject) => {
    if (!mcpProcess || !mcpProcess.stdin.writable) {
      reject(new Error("MCP process not running"));
      return;
    }
    const id = nextId++;
    pendingRequests.set(id, { resolve, reject });
    const req = { jsonrpc: "2.0", id, method, params };
    mcpProcess.stdin.write(JSON.stringify(req) + "\n");
  });
}

// --- Browser lifecycle ---

async function openBrowser() {
  if (browserState === "open") {
    return {
      content: [{ type: "text", text: "Browser is already open." }],
      isError: false,
    };
  }

  try {
    // Ensure user data dir exists
    fs.mkdirSync(USER_DATA_DIR, { recursive: true });

    await startMCPProcess();
    browserState = "open";
    return {
      content: [
        {
          type: "text",
          text: "Browser opened successfully (headed, DISPLAY=:1). Session data persisted in user-data directory.",
        },
      ],
    };
  } catch (err) {
    return {
      content: [
        { type: "text", text: `Failed to open browser: ${err.message}` },
      ],
      isError: true,
    };
  }
}

async function closeBrowser() {
  if (browserState === "closed") {
    return {
      content: [{ type: "text", text: "Browser is already closed." }],
      isError: false,
    };
  }

  if (mcpProcess) {
    mcpProcess.stdin.end();
    mcpProcess.kill("SIGTERM");
    // Give it a moment to exit gracefully
    await new Promise((resolve) => setTimeout(resolve, 1000));
    if (mcpProcess) {
      mcpProcess.kill("SIGKILL");
    }
  }

  browserState = "closed";
  mcpProcess = null;

  return {
    content: [{ type: "text", text: "Browser closed." }],
  };
}

// --- Discover tools from @playwright/mcp ---

let cachedTools = null;

async function discoverTools() {
  if (cachedTools) return cachedTools;

  // If browser is already open, just query the running MCP process
  if (browserState === "open" && mcpProcess) {
    const result = await sendToMCP("tools/list", {});
    cachedTools = result.tools || [];
    return cachedTools;
  }

  // Otherwise, spawn a separate short-lived MCP process with --isolated
  // to discover tools without opening a visible browser window.
  cachedTools = await discoverToolsViaIsolatedProcess();
  return cachedTools;
}

/**
 * Spawn a temporary @playwright/mcp process with --isolated flag
 * just to get tools/list. This doesn't persist state or need DISPLAY.
 */
function discoverToolsViaIsolatedProcess() {
  return new Promise((resolve, reject) => {
    const args = [
      "@playwright/mcp@latest",
      "--isolated",
      "--caps=vision,pdf",
    ];

    const tempProc = spawn("npx", args, {
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, DISPLAY: process.env.DISPLAY || ":1" },
    });

    let stdout = "";
    let settled = false;

    const timeout = setTimeout(() => {
      if (!settled) {
        settled = true;
        tempProc.kill("SIGKILL");
        reject(new Error("discover timeout (15s)"));
      }
    }, 15000);

    tempProc.stdout.on("data", (chunk) => {
      stdout += chunk.toString();
      processDiscoverOutput();
    });

    tempProc.stderr.on("data", (chunk) => {
      console.error(`[discover] ${chunk}`);
    });

    tempProc.on("error", (err) => {
      if (!settled) {
        settled = true;
        clearTimeout(timeout);
        reject(err);
      }
    });

    tempProc.on("exit", () => {
      if (!settled) {
        settled = true;
        clearTimeout(timeout);
        reject(new Error("discover process exited unexpectedly"));
      }
    });

    // State machine for discover handshake
    let phase = "init"; // init -> tools_list -> done
    let initId = 1;
    let toolsId = 2;

    function processDiscoverOutput() {
      let idx;
      while ((idx = stdout.indexOf("\n")) !== -1) {
        const line = stdout.slice(0, idx).trim();
        stdout = stdout.slice(idx + 1);
        if (!line) continue;
        try {
          const msg = JSON.parse(line);
          if (phase === "init" && msg.id === initId) {
            // Initialize succeeded, send notification then tools/list
            phase = "tools_list";
            tempProc.stdin.write(JSON.stringify({jsonrpc:"2.0",method:"notifications/initialized"}) + "\n");
            tempProc.stdin.write(JSON.stringify({jsonrpc:"2.0",id:toolsId,method:"tools/list",params:{}}) + "\n");
          } else if (phase === "tools_list" && msg.id === toolsId) {
            // Got tools list
            phase = "done";
            settled = true;
            clearTimeout(timeout);
            tempProc.stdin.end();
            tempProc.kill("SIGTERM");
            const tools = (msg.result && msg.result.tools) || [];
            resolve(tools);
          }
        } catch (e) {
          console.error("[discover] parse error:", line);
        }
      }
    }

    // Send initialize request
    const initReq = {
      jsonrpc: "2.0",
      id: initId,
      method: "initialize",
      params: {
        protocolVersion: "2024-11-05",
        capabilities: {},
        clientInfo: { name: "browser-daemon-discover", version: "1.0.0" },
      },
    };
    tempProc.stdin.write(JSON.stringify(initReq) + "\n");
  });
}

// --- Socket server ---

function handleConnection(socket) {
  let buffer = "";

  socket.on("data", (chunk) => {
    buffer += chunk.toString();
    let newlineIdx;
    while ((newlineIdx = buffer.indexOf("\n")) !== -1) {
      const line = buffer.slice(0, newlineIdx).trim();
      buffer = buffer.slice(newlineIdx + 1);
      if (line) handleMessage(socket, line);
    }
  });

  socket.on("error", (err) => {
    if (err.code !== "ECONNRESET") {
      console.error("[browser-daemon] Socket error:", err.message);
    }
  });
}

async function handleMessage(socket, line) {
  let req;
  try {
    req = JSON.parse(line);
  } catch {
    writeResponse(socket, null, null, { code: -32700, message: "Parse error" });
    return;
  }

  try {
    switch (req.method) {
      case "initialize":
        writeResponse(socket, req.id, {
          protocolVersion: "2024-11-05",
          capabilities: { tools: {} },
          serverInfo: { name: "mcp-server-browser", version: "1.0.0" },
        });
        break;

      case "notifications/initialized":
        // No response needed
        break;

      case "tools/list":
        await handleToolsList(socket, req);
        break;

      case "tools/call":
        await handleToolsCall(socket, req);
        break;

      default:
        writeResponse(socket, req.id, null, {
          code: -32601,
          message: `Method not found: ${req.method}`,
        });
    }
  } catch (err) {
    writeResponse(socket, req.id, null, {
      code: -32603,
      message: err.message,
    });
  }
}

async function handleToolsList(socket, req) {
  // Our custom tools
  const customTools = [
    {
      name: "open_browser",
      description:
        "Launch Chromium browser (headed, visible on X11 desktop). Must be called before using any other browser tools. Session data (cookies, localStorage) is persisted across open/close cycles.",
      inputSchema: {
        type: "object",
        properties: {},
      },
    },
    {
      name: "close_browser",
      description:
        "Close Chromium browser and release memory. The daemon keeps running. Call open_browser again to restart.",
      inputSchema: {
        type: "object",
        properties: {},
      },
    },
  ];

  // Discover @playwright/mcp tools
  let playwrightTools = [];
  try {
    playwrightTools = await discoverTools();
  } catch (err) {
    console.error(
      "[browser-daemon] Failed to discover playwright tools:",
      err.message
    );
  }

  writeResponse(socket, req.id, {
    tools: [...customTools, ...playwrightTools],
  });
}

async function handleToolsCall(socket, req) {
  const { name, arguments: args } = req.params || {};

  // Handle custom tools
  if (name === "open_browser") {
    const result = await openBrowser();
    writeResponse(socket, req.id, result);
    return;
  }

  if (name === "close_browser") {
    const result = await closeBrowser();
    writeResponse(socket, req.id, result);
    return;
  }

  // All other tools require browser to be open
  if (browserState !== "open") {
    writeResponse(socket, req.id, {
      content: [
        {
          type: "text",
          text: 'Browser is not open. Call open_browser first.',
        },
      ],
      isError: true,
    });
    return;
  }

  // Forward to @playwright/mcp
  try {
    const result = await sendToMCP("tools/call", { name, arguments: args });
    writeResponse(socket, req.id, result);
  } catch (err) {
    writeResponse(socket, req.id, {
      content: [{ type: "text", text: `Browser tool error: ${err.message}` }],
      isError: true,
    });
  }
}

function writeResponse(socket, id, result, error) {
  const resp = { jsonrpc: "2.0", id };
  if (error) {
    resp.error = error;
  } else {
    resp.result = result;
  }
  try {
    socket.write(JSON.stringify(resp) + "\n");
  } catch {
    // Socket may have been closed
  }
}

// --- Main ---

// Clean up stale socket
if (fs.existsSync(SOCKET_PATH)) {
  fs.unlinkSync(SOCKET_PATH);
}

const server = net.createServer(handleConnection);

server.listen(SOCKET_PATH, () => {
  // Make socket accessible by all users in container
  fs.chmodSync(SOCKET_PATH, 0o777);
  console.error(`[browser-daemon] Listening on ${SOCKET_PATH}`);
});

server.on("error", (err) => {
  console.error("[browser-daemon] Server error:", err);
  process.exit(1);
});

// Graceful shutdown
process.on("SIGTERM", async () => {
  console.error("[browser-daemon] SIGTERM received, shutting down");
  await closeBrowser();
  server.close();
  process.exit(0);
});

process.on("SIGINT", async () => {
  console.error("[browser-daemon] SIGINT received, shutting down");
  await closeBrowser();
  server.close();
  process.exit(0);
});
