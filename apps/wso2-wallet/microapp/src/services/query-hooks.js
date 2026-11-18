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

import { useQuery } from "@tanstack/react-query";
import {
  getWalletBalanceByWalletAddress,
  getCurrentBlockNumber,
} from "../services/wallet.service";
import { getUserWalletAddresses, fetchAppConfigs } from "./wallet.service";

export function useWalletBalance(walletAddress, options = {}) {
  const isValidAddress =
    typeof walletAddress === "string" &&
    walletAddress.startsWith("0x") &&
    walletAddress.length === 42;
  return useQuery({
    queryKey: ["walletBalance", walletAddress],
    queryFn: () => getWalletBalanceByWalletAddress(walletAddress),
    enabled: isValidAddress,
    staleTime: 30_000, // Data is considered fresh for 30 seconds; avoids refetching if still fresh
    retry: 2,
    retryDelay: (attemptIndex) => 1000 * (attemptIndex + 1), // Wait 1s, then 2s between retries
    ...options,
  });
}

export function useBlockNumber(options = {}) {
  return useQuery({
    queryKey: ["blockNumber"],
    queryFn: getCurrentBlockNumber,
    staleTime: 10_000, // Data is considered fresh for 10 seconds
    ...options,
  });
}

export function useUserWallets(walletAddress, options = {}) {
  return useQuery({
    queryKey: ["userWallets", walletAddress || ""],
    queryFn: getUserWalletAddresses,
    enabled: !!walletAddress,
    staleTime: 1000 * 60,
    retry: 2,
    retryDelay: (attemptIndex) => 1000 * (attemptIndex + 1),
    ...options,
  });
}

// Whether the signed-in user may share wallets. Shares the app-config query with the
// maintenance banner, so it costs no extra request. False until the answer arrives.
export function useCanShareWallets() {
  const { data } = useQuery({
    queryKey: ["app-configs"],
    queryFn: fetchAppConfigs,
    refetchInterval: 30000,
  });
  return !!data?.canShareWallets;
}
