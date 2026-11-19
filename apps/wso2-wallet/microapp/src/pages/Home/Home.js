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

import {
  SendOutlined,
  DownloadOutlined,
  LoadingOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';

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
  PERSONAL_WALLET,
  SHARED_WALLET,
  WALLET_ENDING_IN,
  ACCESS_ONLY_YOU,
  ACCESS_SHARED_OWNER,
  ACCESS_SHARED_TRANSACT,
  ACCESS_SHARED_READ,
  REQUEST,
  TOTAL_BALANCE,
  WSO2_TOKEN,
  WALLET_APP_NAME,
  CONNECTED,
  CONNECTING,
  OK,
} from '../../constants/strings';
import { showAlertBox } from '../../helpers/alerts';
import { getLocalDataAsync, saveLocalDataAsync } from '../../helpers/storage';
import { waitForBridge } from '../../helpers/bridge';
import { useWalletBalance, useCanShareWallets } from '../../services/query-hooks';
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
      // Someone who only has wallets shared with them still needs their own, so
      // onboarding runs until they own one.
      const ownsWallet = wallets?.some((w) => w.role === WALLET_ROLE.OWNER);
      if (!ownsWallet) {
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
  // People outside the organization cannot share, so "Only you" would tell them
  // nothing; they only see an access pill on a wallet that is actually shared.
  const canShareWallets = useCanShareWallets();
  // Which wallet this is: a named or unnamed shared wallet, or the user's own.
  const isSharedWallet =
    !!activeWallet && (activeWallet.shared || activeWallet.role !== WALLET_ROLE.OWNER);
  const identity = !isSharedWallet
    ? { title: PERSONAL_WALLET, access: ACCESS_ONLY_YOU }
    : {
        title: activeWallet.name || SHARED_WALLET,
        access: {
          [WALLET_ROLE.OWNER]: ACCESS_SHARED_OWNER,
          [WALLET_ROLE.TRANSACT]: ACCESS_SHARED_TRANSACT,
          [WALLET_ROLE.READ]: ACCESS_SHARED_READ,
        }[activeWallet.role],
      };

  if (isResolvingWallet) {
    return (
      <div className="home-loading-overlay">
        {contextHolder}
        <LoadingOutlined
          style={{ fontSize: 28, color: 'var(--orange-primary, #FF4600)' }}
          spin
        />
      </div>
    );
  }

  return (
    <div className="home-container">
      {contextHolder}

      <div className="wallet-hero">
        <div className="wallet-hero-top">
          <span className="wallet-hero-label">
            <span
              className={`wallet-hero-status ${hasValidAddress ? 'is-connected' : 'is-connecting'}`}
              role="img"
              aria-label={hasValidAddress ? CONNECTED : CONNECTING}
              title={hasValidAddress ? CONNECTED : CONNECTING}
            />
            {WALLET_APP_NAME}
          </span>
          {activeWallet && (isSharedWallet || canShareWallets) && (
            <span className="wallet-hero-access">
              {isSharedWallet ? <TeamOutlined /> : <UserOutlined />}
              <span>{identity.access}</span>
            </span>
          )}
        </div>
        <div className="wallet-hero-name">
          <span>{activeWallet ? identity.title : WALLET_APP_NAME}</span>
          {hasValidAddress && (
            // Like the last digits on a bank card: enough to tell wallets apart.
            <span
              className="wallet-hero-address"
              aria-label={`${WALLET_ENDING_IN} ${walletAddress.slice(-4)}`}
            >
              <span aria-hidden="true">•••• {walletAddress.slice(-4)}</span>
            </span>
          )}
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
