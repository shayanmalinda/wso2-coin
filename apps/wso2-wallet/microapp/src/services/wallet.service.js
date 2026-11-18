// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import { DateTime } from "luxon";

import { BACKEND_BASE_URL, STORAGE_KEYS } from "../constants/configs";
import { getTokenAsync } from "../helpers/auth";
import { getLocalDataAsync } from "../helpers/storage";

export const MAX_TRANSFER_PAGE = 100;

const request = async (path, { method = "GET", body } = {}) => {
  if (!BACKEND_BASE_URL) {
    throw new Error(
      "Wallet backend URL is not configured (set REACT_APP_WALLET_BACKEND_BASE_URL)"
    );
  }
  const response = await fetch(`${BACKEND_BASE_URL}${path}`, {
    method,
    cache: "no-store",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${await getTokenAsync()}`,
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  if (!response.ok) {
    let message = "Unknown error";
    try {
      message = (await response.json()).message || message;
    } catch (_) {
      // response had no JSON body
    }
    throw new Error(`${response.status} - ${message}`);
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
};

export const WALLET_ROLE = {
  OWNER: "OWNER",
  TRANSACT: "TRANSACT",
  READ: "READ",
};

const toWallet = (w) => ({
  walletAddress: w.address,
  defaultWallet: w.defaultWallet,
  createdOn: w.createdOn,
  name: w.name || "",
  // Older backends send no role; every wallet they return is the caller's own.
  role: w.role || WALLET_ROLE.OWNER,
  shared: !!w.shared,
});

export const getUserWalletAddresses = async () => {
  const wallets = await request("/wallets");
  return (wallets || []).map(toWallet);
};

export const createWallet = async () => toWallet(await request("/wallets", { method: "POST" }));

export const setWalletAsPrimary = (walletAddress) =>
  request(`/wallets/${walletAddress}/set-primary`, { method: "POST" });

export const getWalletBalanceByWalletAddress = async (walletAddress) => {
  const { balance } = await request(`/wallets/${walletAddress}/balance`);
  return balance;
};

// Name of a wallet as shown to someone sending to it; "" when it has none or the
// lookup fails, so a missing name never blocks a transfer.
export const getWalletName = async (walletAddress) => {
  try {
    const info = await request(`/wallets/${walletAddress}/info`);
    return info?.name || "";
  } catch (_) {
    return "";
  }
};

export const renameWallet = (walletAddress, name) =>
  request(`/wallets/${walletAddress}/name`, { method: "POST", body: { name } });

export const getWalletAccess = async (walletAddress) =>
  (await request(`/wallets/${walletAddress}/access`)) || [];

export const grantWalletAccess = (walletAddress, email, accessLevel) =>
  request(`/wallets/${walletAddress}/access`, {
    method: "POST",
    body: { email, accessLevel },
  });

export const revokeWalletAccess = (walletAddress, email) =>
  request(`/wallets/${walletAddress}/access/revoke`, {
    method: "POST",
    body: { email },
  });

// People in the organization matching a name or email, for the share dialog. Returns
// [] when the search is unavailable, so the owner can still type an email.
export const searchEmployees = async (query) => {
  try {
    const params = new URLSearchParams({ q: query });
    return (await request(`/employees?${params.toString()}`)) || [];
  } catch (_) {
    return [];
  }
};

export const leaveWallet = (walletAddress) =>
  request(`/wallets/${walletAddress}/access/leave`, { method: "POST" });

export const getWalletHealth = (walletAddress) => request(`/wallets/${walletAddress}/health`);

export const getCurrentBlockNumber = async () => {
  try {
    const origin = BACKEND_BASE_URL.replace(/\/api\/v1\/?$/, "");
    const response = await fetch(`${origin}/health`, { method: "GET" });
    return response.ok ? Date.now() : null;
  } catch (_) {
    return null;
  }
};

export const transferToken = async (toAddress, amount) => {
  const fromAddress = await getLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS);
  const { transactionHash } = await request("/transfers", {
    method: "POST",
    body: { fromAddress, toAddress, amount },
  });
  return { txHash: transactionHash, transactionHash };
};

const formatTimestamp = (iso) => {
  try {
    return DateTime.fromISO(iso, { zone: "local" }).toFormat("dd LLL yy HH:mm");
  } catch (_) {
    return "Unknown time";
  }
};

export const getTransactionHistory = async (
  walletAddress,
  limit = MAX_TRANSFER_PAGE,
  offset = 0
) => {
  const safeLimit = Math.min(Math.max(limit, 1), MAX_TRANSFER_PAGE);
  const params = new URLSearchParams({
    limit: String(safeLimit),
    offset: String(Math.max(offset, 0)),
  });
  const page = await request(`/wallets/${walletAddress}/transactions?${params.toString()}`);
  const transactions = (page.transactions || []).map((t) => {
    const ms = Date.parse(t.timestamp);
    const blockTimestamp = Number.isFinite(ms) ? ms : null;
    return {
      txHash: t.transactionHash,
      blockNumber: null,
      logIndex: null,
      from: t.fromAddress,
      to: t.toAddress,
      value: t.amount,
      blockTimestamp,
      timestamp: blockTimestamp ? formatTimestamp(new Date(blockTimestamp).toISOString()) : "Unknown time",
      direction: t.direction === "sent" ? "send" : "receive",
      runningBalance: null,
      initiatedBy: t.initiatedBy || "",
    };
  });
  return {
    address: walletAddress,
    transactions,
    totalCount: transactions.length,
    hasMore: page.hasMore,
    currentPage: 1,
    totalPages: 1,
  };
};

export const fetchAppConfigs = async () => {
  try {
    const config = await request("/app-config");
    return {
      isMaintenanceMode: !!config.maintenanceMode,
      maintenanceMessage: config.maintenanceMessage || "",
      // Whether this user may share wallets (they belong to the organization).
      canShareWallets: !!config.walletSharingEnabled,
    };
  } catch (_) {
    // Fail safe: never block the app if the config endpoint is unreachable.
    return { isMaintenanceMode: false, maintenanceMessage: "", canShareWallets: false };
  }
};
