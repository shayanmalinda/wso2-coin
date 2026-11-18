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

import './Home.css';

import {
  useEffect,
  useState,
  useRef,
} from 'react';

import { message } from 'antd';
import { useNavigate } from 'react-router-dom';

import { SendOutlined, DownloadOutlined, LoadingOutlined } from '@ant-design/icons';

import RecentActivities from '../../components/Home/RecentActivities';
import {
  DEFAULT_WALLET_ADDRESS,
  STORAGE_KEYS,
} from '../../constants/configs';
import {
  ERROR,
  ERROR_RETRIEVE_WALLET_ADDRESS,
  ERROR_BRIDGE_NOT_READY,
  SEND,
  READ_ONLY_SEND_INFO,
  REQUEST,
  TOTAL_BALANCE,
  WSO2_TOKEN,
  WSO2_WALLET,
  CONNECTED,
  CONNECTING,
  OK,
} from '../../constants/strings';
import { showAlertBox } from '../../helpers/alerts';
import { getLocalDataAsync, saveLocalDataAsync } from '../../helpers/storage';
import { waitForBridge } from '../../helpers/bridge';
import { useWalletBalance } from '../../services/query-hooks';
import { getUserWalletAddresses, WALLET_ROLE } from '../../services/wallet.service';
import { formatBalance } from '../../utils/transactionUtils';

function Home() {
  const navigate = useNavigate();
  const [walletAddress, setWalletAddress] = useState(DEFAULT_WALLET_ADDRESS);
  const [isResolvingWallet, setIsResolvingWallet] = useState(true);
  // The active wallet as the backend describes it: its name and what the user may do.
  const [activeWallet, setActiveWallet] = useState(null);
  const recentActivitiesRef = useRef();

  const [messageApi, contextHolder] = message.useMessage();

  const fetchWalletAddress = async () => {
    try {
      const isBridgeReady = await waitForBridge();
      if (!isBridgeReady) {
        console.error(ERROR_BRIDGE_NOT_READY);
        showAlertBox(ERROR, ERROR_BRIDGE_NOT_READY, OK);
        setIsResolvingWallet(false);
        return;
      }

      const wallets = await getUserWalletAddresses();
      if (!wallets || wallets.length === 0) {
        // Keep the loading state until the redirect unmounts this page, so the
        // wallet UI never flashes before create-wallet.
        navigate("/create-wallet", { replace: true });
        return;
      }
      // Respect a previously-selected active wallet if it still exists; only fall
      // back to the default (or first) wallet on the very first load.
      const stored = await getLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS);
      const active =
        (stored &&
          wallets.find(
            (w) => w.walletAddress.toLowerCase() === stored.toLowerCase()
          )) ||
        wallets.find((w) => w.defaultWallet) ||
        wallets[0];
      await saveLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS, active.walletAddress);
      setActiveWallet(active);
      if (active.walletAddress !== walletAddress) {
        setWalletAddress(active.walletAddress);
      }
      setIsResolvingWallet(false);
    } catch (error) {
      console.log(`${ERROR_RETRIEVE_WALLET_ADDRESS} - ${error}`);
      messageApi.error(ERROR_RETRIEVE_WALLET_ADDRESS);
      setIsResolvingWallet(false);
    }
  };

  const { data: tokenBalance, isLoading: isTokenBalanceLoading, refetch } = useWalletBalance(walletAddress);
  const hasValidAddress =
    typeof walletAddress === 'string' &&
    walletAddress.startsWith('0x') &&
    walletAddress.length === 42;

  useEffect(() => {
    fetchWalletAddress();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (walletAddress &&
        walletAddress !== DEFAULT_WALLET_ADDRESS &&
        walletAddress !== "0x" &&
        walletAddress.length === 42) {
      refetch();
    }
  }, [walletAddress, refetch]);

  // Pull to refresh reloads the wallet list too, so a change to the user's access
  // (or the wallet's name) shows up without reopening the app.
  const handlePullRefresh = () => Promise.all([refetch(), fetchWalletAddress()]);

  const handleSend = () => {
    navigate("/send");
  };

  const handleReceive = () => {
    navigate("/receive");
  };

  const isReadOnly = activeWallet?.role === WALLET_ROLE.READ;

  if (isResolvingWallet) {
    return (
      <div className="home-loading-overlay">
        {contextHolder}
        <LoadingOutlined
          style={{ fontSize: 28, color: 'var(--orange-primary, #ff7300)' }}
          spin
        />
      </div>
    );
  }

  return (
    <div className="home-container">
      {contextHolder}

      <div className="wallet-hero">
        <div className="wallet-hero-label">Connected Wallet</div>
        <div className="wallet-hero-name">{activeWallet?.name || WSO2_WALLET}</div>
        <div className="wallet-hero-badge-row">
          <span className={`hero-badge ${hasValidAddress ? 'is-connected' : 'is-connecting'}`}>
            <span className="hero-badge-dot" />
            <span className="hero-badge-text">
              {hasValidAddress ? CONNECTED : CONNECTING}
            </span>
          </span>
        </div>
        <div className="hero-balance-label">{TOTAL_BALANCE}</div>
        <div className="hero-balance-row">
          <div className="hero-balance-amount">
            {isTokenBalanceLoading ? (
              <div
                className="hero-balance-skeleton"
                role="status"
                aria-label="Loading balance"
              />
            ) : typeof tokenBalance === 'undefined' ? (
              <button className="hero-balance-retry" onClick={refetch}>
                Retry
              </button>
            ) : (
              formatBalance(tokenBalance)
            )}
          </div>
          {!isTokenBalanceLoading && (
            <div className="hero-balance-ticker">{WSO2_TOKEN}</div>
          )}
        </div>
      </div>

      <div className="home-action-row">
        <button
          className="home-action-btn home-action-btn-primary"
          onClick={handleSend}
          disabled={isReadOnly}
        >
          <SendOutlined style={{ fontSize: 16 }} />
          <span>{SEND}</span>
        </button>
        <button
          className="home-action-btn home-action-btn-secondary"
          onClick={handleReceive}
        >
          <DownloadOutlined style={{ fontSize: 16 }} />
          <span>{REQUEST}</span>
        </button>
      </div>

      {isReadOnly && (
        <div className="home-read-only-note">{READ_ONLY_SEND_INFO}</div>
      )}

      <RecentActivities
        ref={recentActivitiesRef}
        walletAddress={walletAddress}
        onPullRefresh={handlePullRefresh}
      />
    </div>
  );
}

export default Home;
