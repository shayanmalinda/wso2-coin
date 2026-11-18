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

import './Profile.css';

import {
  useEffect,
  useState,
} from 'react';

import { Modal } from 'antd';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { CopyToClipboard } from 'react-copy-to-clipboard';

import {
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LoadingOutlined,
  PlusOutlined,
  QrcodeOutlined,
  RightOutlined,
  SwapOutlined,
  TeamOutlined,
  WalletOutlined,
} from '@ant-design/icons';
import { QRCodeSVG } from 'qrcode.react';

import ConfirmModal from '../../components/ConfirmModal/ConfirmModal';
import WalletAccessModal from '../../components/WalletAccess/WalletAccessModal';
import { STORAGE_KEYS } from '../../constants/configs';
import {
  ERROR,
  ERROR_READING_WALLET_DETAILS,
  ERROR_CREATING_WALLET,
  ERROR_SWITCHING_WALLET,
  ERROR_SETTING_DEFAULT,
  CREATE_A_NEW_WALLET,
  CREATE_WALLET_CONFIRM,
  CREATE,
  CANCEL,
  CONFIRM,
  SUCCESS,
  SUCCESS_WALLET_CREATED,
  OK,
  SHOW_WALLET_ADDRESS,
  WALLET_ADDRESS_COPIED,
  WSO2_TOKEN,
  SWITCH_TO_THIS_WALLET,
  SWITCH_WALLET_INFO,
  ACTIVE_WALLET_INFO,
  CONFIRM_SWITCH_WALLET,
  CONFIRM_SET_DEFAULT,
  CURRENTLY_VIEWING,
  VIEWING_TAG,
  SET_AS_DEFAULT,
  DEFAULT_WALLET_LABEL,
  DEFAULT_TAG,
  DEFAULT_REWARDS_INFO,
  SET_DEFAULT_INFO,
  SWITCHED_TO_WALLET,
  DEFAULT_WALLET_UPDATED,
  SHARED_TAG,
  READ_ONLY_TAG,
  CAN_SEND_TAG,
  MANAGE_ACCESS,
  MANAGE_ACCESS_INFO,
  SHARED_WITH_YOU_INFO,
  LEAVE_WALLET,
  CONFIRM_LEAVE_WALLET,
  LEFT_WALLET,
  ERROR_LEAVING_WALLET,
} from '../../constants/strings';
import { showToast, showAlertBox } from '../../helpers/alerts';
import {
  getLocalDataAsync,
  saveLocalDataAsync,
} from '../../helpers/storage';
import {
  WALLET_ROLE,
  setWalletAsPrimary,
  createWallet,
  leaveWallet,
} from '../../services/wallet.service';
import {
  useCanShareWallets,
  useUserWallets,
  useWalletBalance,
} from '../../services/query-hooks';
import { formatBalance } from '../../utils/transactionUtils';

const formatWalletAddress = (addr) => {
  if (!addr) return '';
  if (addr.length <= 24) return addr;
  return `${addr.slice(0, 12)}...${addr.slice(-10)}`;
};

const isOwner = (wallet) => wallet?.role === WALLET_ROLE.OWNER;

// Tags describing how a wallet is shared: the owner sees "Shared", a person it is
// shared with sees what they can do with it.
function SharingTags({ wallet }) {
  if (isOwner(wallet)) {
    return wallet.shared ? (
      <span className="profile-shared-tag">{SHARED_TAG}</span>
    ) : null;
  }
  return (
    <span className="profile-shared-tag">
      {wallet.role === WALLET_ROLE.READ ? READ_ONLY_TAG : CAN_SEND_TAG}
    </span>
  );
}

// Renders a single wallet's O2C balance in the wallet list. Fetches only when the
// list is expanded (enabled), and reuses the cached balance for the active wallet.
function WalletRowBalance({ address, enabled }) {
  const { data: balance, isLoading } = useWalletBalance(address, {
    enabled,
    refetchOnMount: 'always',
  });
  if (!enabled || isLoading || typeof balance === 'undefined') {
    return <span className="profile-wallet-balance is-loading">—</span>;
  }
  return (
    <span className="profile-wallet-balance">
      {formatBalance(balance)}
      <span className="profile-wallet-balance-ticker">{WSO2_TOKEN}</span>
    </span>
  );
}

function Profile() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const [walletAddress, setWalletAddress] = useState('');
  // Sharing is for people in the organization; others are not offered it.
  const canShareWallets = useCanShareWallets();

  const {
    data: userWallets = [],
    isLoading: isLoadingWallets,
    isError: isUserWalletsError,
  } = useUserWallets(walletAddress, { refetchOnMount: 'always' });

  const [selectedWallet, setSelectedWallet] = useState(null);
  // The list is refetched after sharing changes, so show the fresh copy of the
  // selected wallet (name, Shared tag) rather than the one captured on tap.
  const detailWallet =
    userWallets.find((w) => w.walletAddress === selectedWallet?.walletAddress) ||
    selectedWallet;
  const [isWalletModalOpen, setIsWalletModalOpen] = useState(false);
  const [isSettingPrimary, setIsSettingPrimary] = useState(false);
  const [isSwitching, setIsSwitching] = useState(false);
  const [isCreating, setIsCreating] = useState(false);
  const [isCreateConfirmOpen, setIsCreateConfirmOpen] = useState(false);
  const [confirmAction, setConfirmAction] = useState(null); // 'switch' | 'default' | 'leave'
  const [isLeaving, setIsLeaving] = useState(false);
  const [isAccessModalOpen, setIsAccessModalOpen] = useState(false);

  const [isQrModalOpen, setIsQrModalOpen] = useState(false);
  const [isWalletsExpanded, setIsWalletsExpanded] = useState(false);

  const [isAddressCopied, setIsAddressCopied] = useState(false);

  const fetchWalletDetails = async () => {
    try {
      const walletAddressResponse = await getLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS);
      setWalletAddress(walletAddressResponse);
    } catch (error) {
      console.log(`${ERROR_READING_WALLET_DETAILS} - ${error}`);
    }
  };

  useEffect(() => {
    fetchWalletDetails();
  }, []);

  useEffect(() => {
    if (isUserWalletsError) {
      showAlertBox(ERROR, ERROR_READING_WALLET_DETAILS, OK);
    }
  }, [isUserWalletsError]);

  const handleCopyAddress = () => {
    showToast(SUCCESS, WALLET_ADDRESS_COPIED);
    setIsAddressCopied(true);
    setTimeout(() => setIsAddressCopied(false), 2000);
  };

  // Switches only the locally-active wallet (what Home shows); does not change the
  // server-side default wallet.
  const handleSwitchToWallet = async () => {
    if (!selectedWallet) return;
    setIsSwitching(true);
    try {
      await saveLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS, selectedWallet.walletAddress);
      setWalletAddress(selectedWallet.walletAddress);
      setConfirmAction(null);
      setIsWalletModalOpen(false);
      showToast(SUCCESS, SWITCHED_TO_WALLET);
      navigate('/');
    } catch (error) {
      console.error('Error switching wallet:', error);
      showAlertBox(ERROR, ERROR_SWITCHING_WALLET, OK);
    } finally {
      setIsSwitching(false);
    }
  };

  // Sets only the server-side default wallet (where coin rewards are received); does
  // not change which wallet is locally active.
  const handleSetAsDefault = async () => {
    if (!selectedWallet || selectedWallet.defaultWallet) return;

    setIsSettingPrimary(true);
    try {
      await setWalletAsPrimary(selectedWallet.walletAddress);
      setSelectedWallet((prev) => (prev ? { ...prev, defaultWallet: true } : prev));
      setConfirmAction(null);
      setIsWalletModalOpen(false);
      showToast(SUCCESS, DEFAULT_WALLET_UPDATED);
      await queryClient.invalidateQueries({ queryKey: ['userWallets'] });
      navigate('/');
    } catch (error) {
      console.error('Error setting default wallet:', error);
      showAlertBox(ERROR, ERROR_SETTING_DEFAULT, OK);
    } finally {
      setIsSettingPrimary(false);
    }
  };

  // Gives up the caller's own access to a wallet that was shared with them.
  const handleLeaveWallet = async () => {
    if (!selectedWallet || isOwner(selectedWallet)) return;
    setIsLeaving(true);
    try {
      await leaveWallet(selectedWallet.walletAddress);
      const wasActive =
        walletAddress &&
        selectedWallet.walletAddress.toLowerCase() === walletAddress.toLowerCase();
      if (wasActive) {
        // Home falls back to the default wallet when the stored one is gone. The
        // wallet is already left, so a failed local write must not undo that.
        try {
          await saveLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS, '');
        } catch (storageError) {
          console.error('Error clearing the active wallet:', storageError);
        }
      }
      setConfirmAction(null);
      setIsWalletModalOpen(false);
      showToast(SUCCESS, LEFT_WALLET);
      await queryClient.invalidateQueries({ queryKey: ['userWallets'] });
      if (wasActive) navigate('/');
    } catch (error) {
      console.error('Error leaving wallet:', error);
      showAlertBox(ERROR, ERROR_LEAVING_WALLET, OK);
    } finally {
      setIsLeaving(false);
    }
  };

  const createNewWallet = async () => {
    setIsCreating(true);
    try {
      const wallet = await createWallet();
      await saveLocalDataAsync(STORAGE_KEYS.WALLET_ADDRESS, wallet.walletAddress);
      setWalletAddress(wallet.walletAddress);
      showToast(SUCCESS, SUCCESS_WALLET_CREATED);
      await queryClient.invalidateQueries({ queryKey: ['userWallets'] });
      setIsCreateConfirmOpen(false);
      navigate('/');
    } catch (error) {
      console.error('Error creating wallet:', error);
      showAlertBox(ERROR, ERROR_CREATING_WALLET, OK);
    } finally {
      setIsCreating(false);
    }
  };

  return (
    <div className="profile-page">
      {/* Wallet Details Modal */}
      <Modal
        open={isWalletModalOpen}
        onCancel={() => setIsWalletModalOpen(false)}
        footer={null}
        title="Wallet Details"
        centered
      >
        {detailWallet && (() => {
          const isActive =
            walletAddress &&
            detailWallet.walletAddress &&
            detailWallet.walletAddress.toLowerCase() ===
              walletAddress.toLowerCase();
          const isPrimary = detailWallet.defaultWallet;
          const owned = isOwner(detailWallet);
          const showTagRow = isActive || isPrimary || !owned || detailWallet.shared;
          return (
            <div className="profile-modal">
              {detailWallet.name && (
                <div className="profile-modal-name">{detailWallet.name}</div>
              )}
              <CopyToClipboard
                text={detailWallet.walletAddress}
                onCopy={handleCopyAddress}
              >
                <button type="button" className="profile-modal-address">
                  <span className="profile-modal-address-text">
                    {detailWallet.walletAddress}
                  </span>
                  {isAddressCopied ? <CheckOutlined /> : <CopyOutlined />}
                </button>
              </CopyToClipboard>
              <div className="profile-modal-meta">
                Created on{' '}
                {new Date(detailWallet.createdOn).toLocaleString()}
              </div>

              {showTagRow && (
                <div className="profile-modal-tag-row">
                  {isPrimary && (
                    <span className="profile-primary-tag">{DEFAULT_TAG}</span>
                  )}
                  {isActive && (
                    <span className="profile-active-tag">{CURRENTLY_VIEWING}</span>
                  )}
                  <SharingTags wallet={detailWallet} />
                </div>
              )}

              <div className="profile-modal-actions">
                <div className="profile-modal-action">
                  <button
                    type="button"
                    className="profile-modal-primary-btn"
                    onClick={() => setConfirmAction('switch')}
                    disabled={isActive || isSwitching}
                  >
                    {isSwitching ? (
                      <LoadingOutlined style={{ fontSize: 14 }} spin />
                    ) : isActive ? (
                      <CheckOutlined style={{ fontSize: 14 }} />
                    ) : (
                      <SwapOutlined style={{ fontSize: 14 }} />
                    )}
                    <span>
                      {isActive ? CURRENTLY_VIEWING : SWITCH_TO_THIS_WALLET}
                    </span>
                  </button>
                  <div className="profile-modal-action-hint">
                    {isActive ? ACTIVE_WALLET_INFO : SWITCH_WALLET_INFO}
                  </div>
                </div>
                {owned ? (
                  <>
                <div className="profile-modal-action">
                  <button
                    type="button"
                    className="profile-modal-secondary-btn"
                    onClick={() => setConfirmAction('default')}
                    disabled={isPrimary || isSettingPrimary}
                  >
                    {isSettingPrimary ? (
                      <LoadingOutlined style={{ fontSize: 14 }} spin />
                    ) : isPrimary ? (
                      <CheckOutlined style={{ fontSize: 14 }} />
                    ) : null}
                    <span>{isPrimary ? DEFAULT_WALLET_LABEL : SET_AS_DEFAULT}</span>
                  </button>
                  <div className="profile-modal-action-hint">
                    {isPrimary ? DEFAULT_REWARDS_INFO : SET_DEFAULT_INFO}
                  </div>
                </div>
                {(canShareWallets || detailWallet.shared) && (
                <div className="profile-modal-action">
                  <button
                    type="button"
                    className="profile-modal-outline-btn"
                    onClick={() => setIsAccessModalOpen(true)}
                  >
                    <TeamOutlined style={{ fontSize: 14 }} />
                    <span>{MANAGE_ACCESS}</span>
                  </button>
                  <div className="profile-modal-action-hint">
                    {MANAGE_ACCESS_INFO}
                  </div>
                </div>
                )}
                  </>
                ) : (
                  <div className="profile-modal-action">
                    <button
                      type="button"
                      className="profile-modal-outline-btn is-danger"
                      onClick={() => setConfirmAction('leave')}
                      disabled={isLeaving}
                    >
                      <span>{LEAVE_WALLET}</span>
                    </button>
                    <div className="profile-modal-action-hint">
                      {SHARED_WITH_YOU_INFO}
                    </div>
                  </div>
                )}
              </div>
            </div>
          );
        })()}
      </Modal>

      {/* QR Code Modal */}
      <Modal
        open={isQrModalOpen}
        onCancel={() => setIsQrModalOpen(false)}
        footer={null}
        title="Wallet QR Code"
        centered
      >
        <div className="profile-qr-modal">
          <div className="profile-qr-subtitle">Share this QR code to receive coins</div>
          <div className="profile-qr-canvas">
            <QRCodeSVG
              value={JSON.stringify({ wallet_address: walletAddress })}
              size={200}
              level="M"
            />
          </div>
          <CopyToClipboard text={walletAddress} onCopy={handleCopyAddress}>
            <button className="profile-qr-address" type="button">
              <span className="profile-qr-address-text">{walletAddress}</span>
              {isAddressCopied ? <CheckOutlined /> : <CopyOutlined />}
            </button>
          </CopyToClipboard>
        </div>
      </Modal>

      {/* Create Wallet Confirmation */}
      <ConfirmModal
        open={isCreateConfirmOpen}
        title={CREATE_A_NEW_WALLET}
        description={CREATE_WALLET_CONFIRM}
        confirmText={CREATE}
        cancelText={CANCEL}
        loading={isCreating}
        onConfirm={createNewWallet}
        onCancel={() => setIsCreateConfirmOpen(false)}
      />

      {/* Switch / Set-default / Leave Confirmation */}
      {(() => {
        const confirms = {
          switch: [SWITCH_TO_THIS_WALLET, CONFIRM_SWITCH_WALLET, handleSwitchToWallet],
          default: [SET_AS_DEFAULT, CONFIRM_SET_DEFAULT, handleSetAsDefault],
          leave: [LEAVE_WALLET, CONFIRM_LEAVE_WALLET, handleLeaveWallet],
        };
        const [title, description, onConfirm] = confirms[confirmAction] || [];
        return (
          <ConfirmModal
            open={confirmAction !== null}
            title={title}
            description={description}
            confirmText={CONFIRM}
            cancelText={CANCEL}
            loading={isSwitching || isSettingPrimary || isLeaving}
            onConfirm={onConfirm}
            onCancel={() => setConfirmAction(null)}
          />
        );
      })()}

      {/* Naming and sharing, for wallets the user owns */}
      <WalletAccessModal
        open={isAccessModalOpen}
        wallet={detailWallet}
        canShare={canShareWallets}
        onClose={() => setIsAccessModalOpen(false)}
      />

      <div className="profile-section">
        <div className="profile-group-label">Wallet</div>
        <div className="profile-card">
          <button
            type="button"
            className="profile-btn"
            onClick={() => setIsQrModalOpen(true)}
          >
            <span className="profile-btn-icon neutral">
              <QrcodeOutlined style={{ fontSize: 18, color: '#0B1633' }} />
            </span>
            <span className="profile-btn-text">
              <span className="profile-btn-label">{SHOW_WALLET_ADDRESS}</span>
              <span className="profile-btn-sub">View your public QR code</span>
            </span>
            <RightOutlined className="profile-btn-chevron" />
          </button>

          <button
            type="button"
            className="profile-btn"
            onClick={() => setIsCreateConfirmOpen(true)}
            disabled={isCreating}
          >
            <span className="profile-btn-icon neutral">
              {isCreating ? (
                <LoadingOutlined style={{ fontSize: 18, color: '#0B1633' }} spin />
              ) : (
                <PlusOutlined style={{ fontSize: 18, color: '#0B1633' }} />
              )}
            </span>
            <span className="profile-btn-text">
              <span className="profile-btn-label">{CREATE_A_NEW_WALLET}</span>
              <span className="profile-btn-sub">Add another wallet to your account</span>
            </span>
            <RightOutlined className="profile-btn-chevron" />
          </button>
        </div>

        <div className="profile-group-label">My Wallets</div>
        {(() => {
          const isActiveWallet = (w) =>
            walletAddress &&
            w.walletAddress &&
            w.walletAddress.toLowerCase() === walletAddress.toLowerCase();
          const activeWallet = userWallets.find(isActiveWallet);
          const primaryWallet = userWallets.find((w) => w.defaultWallet);
          const count = userWallets.length;
          const summaryLabel = isLoadingWallets
            ? 'Loading…'
            : count === 0
              ? 'No wallets found'
              : `${count} wallet${count === 1 ? '' : 's'}`;
          const summarySub = isLoadingWallets
            ? 'Fetching your wallets'
            : count === 0
              ? 'Nothing to display yet'
              : activeWallet
                ? `${VIEWING_TAG} ${formatWalletAddress(activeWallet.walletAddress)}`
                : primaryWallet
                  ? `${DEFAULT_TAG} ${formatWalletAddress(primaryWallet.walletAddress)}`
                  : 'Tap to view all';
          const canExpand = !isLoadingWallets && count > 0;
          const isOpen = canExpand && isWalletsExpanded;

          return (
            <div className="profile-card profile-wallets-card">
              <button
                type="button"
                className="profile-btn"
                onClick={() =>
                  canExpand && setIsWalletsExpanded((prev) => !prev)
                }
                disabled={!canExpand}
                aria-expanded={isWalletsExpanded}
              >
                <span className="profile-btn-icon neutral">
                  {isLoadingWallets ? (
                    <LoadingOutlined
                      style={{ fontSize: 18, color: '#0B1633' }}
                      spin
                    />
                  ) : (
                    <WalletOutlined
                      style={{ fontSize: 18, color: '#0B1633' }}
                    />
                  )}
                </span>
                <span className="profile-btn-text">
                  <span className="profile-btn-label">{summaryLabel}</span>
                  <span className="profile-btn-sub">{summarySub}</span>
                </span>
                {canExpand && (
                  <DownOutlined
                    className="profile-btn-chevron"
                    style={{
                      transform: `rotate(${isWalletsExpanded ? 180 : 0}deg)`,
                      transition: 'transform 0.25s ease',
                    }}
                  />
                )}
              </button>

              <div
                className={`profile-wallets-body ${isOpen ? 'is-open' : ''}`}
                aria-hidden={!isOpen}
              >
                <div className="profile-wallets-body-inner">
                  <div className="profile-wallets-list">
                    {userWallets.map((wallet, idx) => {
                      const isActive = isActiveWallet(wallet);
                      return (
                        <button
                          key={wallet.walletAddress + idx}
                          type="button"
                          tabIndex={isOpen ? 0 : -1}
                          className={`profile-wallet-row ${isActive ? 'is-active' : ''}`}
                          onClick={() => {
                            setSelectedWallet(wallet);
                            setIsWalletModalOpen(true);
                          }}
                        >
                          <span className="profile-wallet-info">
                            {wallet.name && (
                              <span className="profile-wallet-name">{wallet.name}</span>
                            )}
                            <span className="profile-wallet-addr">
                              {formatWalletAddress(wallet.walletAddress)}
                            </span>
                            <WalletRowBalance
                              address={wallet.walletAddress}
                              enabled={isOpen}
                            />
                            <span className="profile-wallet-date">
                              Created {new Date(wallet.createdOn).toLocaleDateString()}
                            </span>
                          </span>
                          <span className="profile-wallet-tags">
                            {isActive && (
                              <span className="profile-active-tag">{VIEWING_TAG}</span>
                            )}
                            {wallet.defaultWallet && (
                              <span className="profile-primary-tag">{DEFAULT_TAG}</span>
                            )}
                            <SharingTags wallet={wallet} />
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              </div>
            </div>
          );
        })()}
      </div>
    </div>
  );
}

export default Profile;
