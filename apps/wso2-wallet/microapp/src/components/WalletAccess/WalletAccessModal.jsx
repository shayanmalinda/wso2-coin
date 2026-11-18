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
  CheckCircleFilled,
  DeleteOutlined,
  EyeOutlined,
  LoadingOutlined,
  PlusOutlined,
  SendOutlined,
  UserOutlined,
} from "@ant-design/icons";

import ConfirmModal from "../ConfirmModal/ConfirmModal";
import {
  ACCESS_LEVEL_READ,
  ACCESS_LEVEL_READ_INFO,
  ACCESS_LEVEL_TRANSACT,
  ACCESS_LEVEL_TRANSACT_INFO,
  ADD_A_PERSON,
  PEOPLE_SUGGESTIONS,
  NO_PEOPLE_FOUND,
  SEARCH_PEOPLE_HINT,
  ACCESS_LEVEL_QUESTION,
  OWNER_TAG,
  YOU,
  WALLET_NAME_INFO,
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
  searchEmployees,
} from "../../services/wallet.service";

const MAX_NAME_LENGTH = 50;
// The backend ignores shorter searches; match it so no request is wasted.
const MIN_SEARCH_LENGTH = 3;
const SEARCH_DELAY_MS = 300;

// The service throws "<status> - <message>". A 400 carries a message written for
// the user (e.g. the person is outside the organization), so show that one.
const errorText = (error, fallback) => {
  const match = /^400 - (.+)$/.exec(error?.message || "");
  return match ? match[1] : fallback;
};

const LEVELS = [
  {
    value: WALLET_ROLE.READ,
    label: ACCESS_LEVEL_READ,
    info: ACCESS_LEVEL_READ_INFO,
    Icon: EyeOutlined,
  },
  {
    value: WALLET_ROLE.TRANSACT,
    label: ACCESS_LEVEL_TRANSACT,
    info: ACCESS_LEVEL_TRANSACT_INFO,
    Icon: SendOutlined,
  },
];

// Arrow keys move between the options of a radio group, as they do for native radios.
const onRadioKeyDown = (e, value, onChange) => {
  const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
  if (!step) return;
  e.preventDefault();
  const i = LEVELS.findIndex((l) => l.value === value);
  onChange(LEVELS[(i + step + LEVELS.length) % LEVELS.length].value);
};

// A person's photo, falling back to their initial when there is none or it fails to
// load. The photo host refuses requests that carry this page as the referrer, so the
// policy is set before `src`: Safari starts the request as soon as `src` is set.
function PersonAvatar({ person }) {
  const [failed, setFailed] = useState(false);
  if (!person.thumbnail || failed) {
    return (
      <span className="wallet-access-avatar">
        {(person.name || person.email).charAt(0).toUpperCase()}
      </span>
    );
  }
  return (
    <img
      referrerPolicy="no-referrer"
      className="wallet-access-avatar"
      src={person.thumbnail}
      alt=""
      onError={() => setFailed(true)}
    />
  );
}

// The two access levels as cards, each saying what the person will be able to do.
function LevelCards({ value, onChange, disabled, label }) {
  return (
    <div className="wallet-access-cards" role="radiogroup" aria-label={label}>
      {LEVELS.map(({ value: v, label: text, info, Icon }) => {
        const selected = v === value;
        return (
          <button
            key={v}
            type="button"
            role="radio"
            aria-checked={selected}
            tabIndex={selected ? 0 : -1}
            className={`wallet-access-card ${selected ? "is-selected" : ""}`}
            onClick={() => onChange(v)}
            onKeyDown={(e) => onRadioKeyDown(e, value, onChange)}
            disabled={disabled}
          >
            <span className="wallet-access-card-icon">
              <Icon />
            </span>
            <span className="wallet-access-card-text">
              <span className="wallet-access-card-title">{text}</span>
              <span className="wallet-access-card-info">{info}</span>
            </span>
            {selected && <CheckCircleFilled className="wallet-access-card-check" />}
          </button>
        );
      })}
    </div>
  );
}

// Compact two-way switch for changing the level of someone who already has access.
function LevelToggle({ value, onChange, disabled, label }) {
  return (
    <div className="wallet-access-toggle" role="radiogroup" aria-label={label}>
      {LEVELS.map(({ value: v, label: text }) => {
        const selected = v === value;
        return (
          <button
            key={v}
            type="button"
            role="radio"
            aria-checked={selected}
            tabIndex={selected ? 0 : -1}
            className={`wallet-access-toggle-option ${selected ? "is-selected" : ""}`}
            onClick={() => !selected && onChange(v)}
            onKeyDown={(e) => onRadioKeyDown(e, value, onChange)}
            disabled={disabled}
          >
            {text}
          </button>
        );
      })}
    </div>
  );
}

// Owner-only dialog for naming a wallet and choosing who else can use it.
// `canShare` is false when sharing is not offered to this owner (for example the
// organization domain changed); existing grants can then be reviewed and removed,
// but not added or changed.
function WalletAccessModal({ open, wallet, onClose, canShare = true }) {
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

  // Search the directory a moment after the owner stops typing. Picking a person
  // fills in their email; `pickedEmail` then keeps the list closed until they type
  // something else.
  const [term, setTerm] = useState("");
  const [pickedEmail, setPickedEmail] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setTerm(email.trim()), SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [email]);
  const canSearch =
    open && term.length >= MIN_SEARCH_LENGTH && term === email.trim() && term !== pickedEmail;
  const { data: suggestions = [], isFetched: searched } = useQuery({
    queryKey: ["employees", term.toLowerCase()],
    queryFn: () => searchEmployees(term),
    enabled: canSearch,
    staleTime: 60_000,
  });
  const alreadyAdded = new Set(grants.map((g) => g.email.toLowerCase()));
  const shownSuggestions = canSearch
    ? suggestions.filter((p) => !alreadyAdded.has(p.email.toLowerCase()))
    : [];
  // Typed text that looks like an email is being entered by hand, not searched.
  const searchedWithNoMatch =
    canSearch && searched && shownSuggestions.length === 0 && !email.includes("@");

  // Reset the form only when the dialog opens or moves to another wallet. The wallet
  // list is refetched after every change, and resetting then would wipe what the
  // user is typing.
  useEffect(() => {
    if (open) {
      setEmail("");
      setPickedEmail("");
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
      if (successText) showToast(SUCCESS, successText);
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
  // Changing a level writes a grant, so it needs a successful read of the current
  // grants (a failed read leaves a stale row that may already be removed).
  const grantLocked = locked || isError || !canShare;
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
          <section className="wallet-access-block">
            <h3 className="wallet-access-heading">{PEOPLE_WITH_ACCESS}</h3>
            <ul className="wallet-access-list">
              <li className="wallet-access-person">
                <span className="wallet-access-avatar is-owner">
                  <UserOutlined />
                </span>
                <span className="wallet-access-who">
                  <span className="wallet-access-email">{YOU}</span>
                </span>
                <span className="wallet-access-owner-tag">{OWNER_TAG}</span>
              </li>
              {grants.map((g) => (
                <li key={g.email} className="wallet-access-person is-member">
                  <span className="wallet-access-avatar">
                    {g.email.charAt(0).toUpperCase()}
                  </span>
                  <span className="wallet-access-who">
                    <span className="wallet-access-email">{g.email}</span>
                    <LevelToggle
                      value={g.accessLevel}
                      label={`Access level for ${g.email}`}
                      disabled={grantLocked}
                      onChange={(next) =>
                        run(
                          g.email,
                          () => grantWalletAccess(address, g.email, next),
                          // The switch itself shows the new level; a toast per
                          // tap only piles up.
                          null,
                          ERROR_UPDATING_ACCESS,
                        )
                      }
                    />
                  </span>
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
            {isLoading ? (
              <div className="wallet-access-hint">
                <LoadingOutlined spin />
              </div>
            ) : isError ? (
              <div className="wallet-access-hint is-error">
                <span>{ERROR_LOADING_ACCESS}</span>
                <button
                  type="button"
                  className="wallet-access-link"
                  onClick={() => refetch()}
                  disabled={isFetching}
                >
                  {RETRY}
                </button>
              </div>
            ) : (
              !isShared && (
                <div className="wallet-access-hint">{NO_ONE_HAS_ACCESS}</div>
              )
            )}
          </section>

          <section className="wallet-access-block">
            <h3 className="wallet-access-heading">{ADD_A_PERSON}</h3>
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
              disabled={busy === "add" || !canShare}
              role="combobox"
              aria-expanded={shownSuggestions.length > 0}
              aria-controls="wallet-access-suggestions"
              aria-autocomplete="list"
            />
            {shownSuggestions.length > 0 && (
              <ul
                id="wallet-access-suggestions"
                className="wallet-access-suggestions"
                role="listbox"
                aria-label={PEOPLE_SUGGESTIONS}
              >
                {shownSuggestions.map((person) => (
                  <li key={person.email} role="presentation">
                    <button
                      type="button"
                      role="option"
                      aria-selected={false}
                      className="wallet-access-suggestion"
                      onClick={() => {
                        setEmail(person.email);
                        setPickedEmail(person.email);
                      }}
                    >
                      <PersonAvatar person={person} />
                      <span className="wallet-access-who is-compact">
                        <span className="wallet-access-email">
                          {person.name || person.email}
                        </span>
                        <span className="wallet-access-suggestion-email">
                          {person.email}
                        </span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {shownSuggestions.length === 0 && (
              <div className="wallet-access-hint">
                {searchedWithNoMatch ? NO_PEOPLE_FOUND : SEARCH_PEOPLE_HINT}
              </div>
            )}
            <div className="wallet-access-sublabel">{ACCESS_LEVEL_QUESTION}</div>
            <LevelCards
              value={level}
              onChange={setLevel}
              disabled={busy === "add"}
              label={ACCESS_LEVEL_QUESTION}
            />
            <button
              type="button"
              className="wallet-access-btn is-primary is-block"
              onClick={handleAdd}
              disabled={!email.trim() || grantLocked}
            >
              {busy === "add" ? <LoadingOutlined spin /> : <PlusOutlined />}
              <span>{ADD_PERSON}</span>
            </button>
          </section>

          <section className="wallet-access-block">
            <label className="wallet-access-heading" htmlFor="wallet-access-name">
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
            <div className="wallet-access-hint">
              {isShared || isLoading || isError
                ? WALLET_NAME_INFO
                : WALLET_NAME_NEEDS_SHARING}
            </div>
          </section>
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
