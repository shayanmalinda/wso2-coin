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

import "./WalletAccessModal.css";

import { useEffect, useState } from "react";

import { Modal } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  DeleteOutlined,
  LoadingOutlined,
  PlusOutlined,
} from "@ant-design/icons";

import ConfirmModal from "../ConfirmModal/ConfirmModal";
import {
  ACCESS_LEVEL_READ,
  ACCESS_LEVEL_TRANSACT,
  ACCESS_REMOVED,
  ACCESS_UPDATED,
  ADD_PERSON,
  ADD_PERSON_PLACEHOLDER,
  ALREADY_HAS_ACCESS,
  CANCEL,
  CONFIRM_REMOVE_ACCESS,
  ERROR,
  ERROR_LOADING_ACCESS,
  ERROR_SAVING_WALLET_NAME,
  ERROR_UPDATING_ACCESS,
  MANAGE_ACCESS,
  NO_ONE_HAS_ACCESS,
  PEOPLE_WITH_ACCESS,
  REMOVE,
  REMOVE_ACCESS,
  RETRY,
  SAVE,
  SUCCESS,
  WALLET_NAME,
  WALLET_NAME_NEEDS_SHARING,
  WALLET_NAME_PLACEHOLDER,
  WALLET_NAME_SAVED,
} from "../../constants/strings";
import { showToast } from "../../helpers/alerts";
import {
  WALLET_ROLE,
  getWalletAccess,
  grantWalletAccess,
  renameWallet,
  revokeWalletAccess,
} from "../../services/wallet.service";

const MAX_NAME_LENGTH = 50;

// The service throws "<status> - <message>". A 400 carries a message written for
// the user (e.g. the person is outside the organization), so show that one.
const errorText = (error, fallback) => {
  const match = /^400 - (.+)$/.exec(error?.message || "");
  return match ? match[1] : fallback;
};

function LevelSelect({ value, onChange, disabled, label }) {
  return (
    <select
      className="wallet-access-select"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      aria-label={label}
    >
      <option value={WALLET_ROLE.READ}>{ACCESS_LEVEL_READ}</option>
      <option value={WALLET_ROLE.TRANSACT}>{ACCESS_LEVEL_TRANSACT}</option>
    </select>
  );
}

// Owner-only dialog for naming a wallet and choosing who else can use it.
function WalletAccessModal({ open, wallet, onClose }) {
  const queryClient = useQueryClient();
  const address = wallet?.walletAddress || "";

  const {
    data: grants = [],
    isLoading,
    isError,
    isFetching,
    refetch,
  } = useQuery({
    queryKey: ["walletAccess", address],
    queryFn: () => getWalletAccess(address),
    enabled: open && !!address,
    refetchOnMount: "always",
  });

  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [level, setLevel] = useState(WALLET_ROLE.READ);
  const [busy, setBusy] = useState(""); // "name" | "add" | an email being changed
  const [toRemove, setToRemove] = useState(null);

  const savedName = wallet?.name || "";

  // Reset the form only when the dialog opens or moves to another wallet. The wallet
  // list is refetched after every change, and resetting then would wipe what the
  // user is typing.
  useEffect(() => {
    if (open) {
      setEmail("");
      setLevel(WALLET_ROLE.READ);
      setToRemove(null);
    }
  }, [open, address]);

  useEffect(() => {
    if (open) setName(savedName);
  }, [open, address, savedName]);

  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ["walletAccess", address] }),
      queryClient.invalidateQueries({ queryKey: ["userWallets"] }),
    ]).catch((error) => console.error("Failed to refresh wallet access", error));

  const run = async (key, action, successText, failureText) => {
    setBusy(key);
    try {
      await action();
      // The write is done. Reload in the background so a slow refresh cannot keep
      // the dialog stuck; the controls stay locked while it loads (see isFetching).
      refresh();
      showToast(SUCCESS, successText);
      return true;
    } catch (error) {
      console.error(failureText, error);
      showToast(ERROR, errorText(error, failureText));
      return false;
    } finally {
      setBusy("");
    }
  };

  const handleAdd = async () => {
    const trimmed = email.trim();
    if (!trimmed) return;
    // Add would silently overwrite an existing member's level, so send the owner to
    // the level selector instead.
    if (grants.some((g) => g.email.toLowerCase() === trimmed.toLowerCase())) {
      showToast(ERROR, ALREADY_HAS_ACCESS);
      return;
    }
    const added = await run(
      "add",
      () => grantWalletAccess(address, trimmed, level),
      ACCESS_UPDATED,
      ERROR_UPDATING_ACCESS,
    );
    if (added) setEmail("");
  };

  const handleRemove = async () => {
    const target = toRemove;
    const removed = await run(
      target,
      () => revokeWalletAccess(address, target),
      ACCESS_REMOVED,
      ERROR_UPDATING_ACCESS,
    );
    if (removed) setToRemove(null);
  };

  // A failed read means we do not know who has access, which is not the same as nobody.
  const isShared = !isError && grants.length > 0;
  const locked = !!busy || isFetching;
  const trimmedName = name.trim();
  const nameChanged = trimmedName !== savedName;

  return (
    <>
      <Modal
        open={open}
        onCancel={busy ? undefined : onClose}
        footer={null}
        title={MANAGE_ACCESS}
        centered
      >
        <div className="wallet-access">
          <label className="wallet-access-label" htmlFor="wallet-access-name">
            {WALLET_NAME}
          </label>
          <div className="wallet-access-row">
            <input
              id="wallet-access-name"
              className="wallet-access-input"
              value={name}
              maxLength={MAX_NAME_LENGTH}
              placeholder={WALLET_NAME_PLACEHOLDER}
              onChange={(e) => setName(e.target.value)}
              disabled={!isShared || busy === "name"}
            />
            <button
              type="button"
              className="wallet-access-btn"
              onClick={() =>
                run(
                  "name",
                  () => renameWallet(address, trimmedName),
                  WALLET_NAME_SAVED,
                  ERROR_SAVING_WALLET_NAME,
                )
              }
              disabled={!isShared || !nameChanged || locked}
            >
              {busy === "name" ? <LoadingOutlined spin /> : SAVE}
            </button>
          </div>
          {!isShared && !isLoading && !isError && (
            <div className="wallet-access-hint">{WALLET_NAME_NEEDS_SHARING}</div>
          )}

          <div className="wallet-access-label wallet-access-section">
            {PEOPLE_WITH_ACCESS}
          </div>
          {isLoading ? (
            <div className="wallet-access-hint">
              <LoadingOutlined spin />
            </div>
          ) : isError ? (
            <div className="wallet-access-hint is-error">
              {ERROR_LOADING_ACCESS}{" "}
              <button
                type="button"
                className="wallet-access-btn"
                onClick={() => refetch()}
                disabled={isFetching}
              >
                {RETRY}
              </button>
            </div>
          ) : !isShared ? (
            <div className="wallet-access-hint">{NO_ONE_HAS_ACCESS}</div>
          ) : (
            <ul className="wallet-access-list">
              {grants.map((g) => (
                <li key={g.email} className="wallet-access-person">
                  <span className="wallet-access-email">{g.email}</span>
                  <LevelSelect
                    value={g.accessLevel}
                    label={`Access level for ${g.email}`}
                    disabled={locked}
                    onChange={(next) =>
                      run(
                        g.email,
                        () => grantWalletAccess(address, g.email, next),
                        ACCESS_UPDATED,
                        ERROR_UPDATING_ACCESS,
                      )
                    }
                  />
                  <button
                    type="button"
                    className="wallet-access-remove"
                    onClick={() => setToRemove(g.email)}
                    disabled={locked}
                    aria-label={`${REMOVE_ACCESS} for ${g.email}`}
                  >
                    {busy === g.email ? (
                      <LoadingOutlined spin />
                    ) : (
                      <DeleteOutlined />
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="wallet-access-add">
            <input
              className="wallet-access-input"
              type="email"
              inputMode="email"
              autoCapitalize="none"
              autoCorrect="off"
              value={email}
              placeholder={ADD_PERSON_PLACEHOLDER}
              aria-label="Email of the person to add"
              onChange={(e) => setEmail(e.target.value)}
              disabled={busy === "add"}
            />
            <div className="wallet-access-row">
              <LevelSelect
                value={level}
                onChange={setLevel}
                disabled={busy === "add"}
                label="Access level for the new person"
              />
              <button
                type="button"
                className="wallet-access-btn is-primary"
                onClick={handleAdd}
                disabled={!email.trim() || locked}
              >
                {busy === "add" ? (
                  <LoadingOutlined spin />
                ) : (
                  <PlusOutlined />
                )}
                <span>{ADD_PERSON}</span>
              </button>
            </div>
          </div>
        </div>
      </Modal>

      <ConfirmModal
        open={toRemove !== null}
        title={REMOVE_ACCESS}
        description={`${toRemove || ""} ${CONFIRM_REMOVE_ACCESS}`}
        confirmText={REMOVE}
        cancelText={CANCEL}
        loading={!!busy}
        onConfirm={handleRemove}
        onCancel={() => setToRemove(null)}
      />
    </>
  );
}

export default WalletAccessModal;
