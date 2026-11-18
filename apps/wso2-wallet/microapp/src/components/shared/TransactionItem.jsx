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

import React, { useMemo, useState } from 'react';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CopyOutlined,
  DownOutlined,
} from '@ant-design/icons';
import { DateTime } from 'luxon';

import { WSO2_TOKEN } from '../../constants/strings';
import {
  formatWalletAddress,
  copyTextToClipboard,
  formatBalance,
} from '../../utils/transactionUtils';

const DetailRow = ({ label, value, copyValue, copyLabel, muted }) => {
  const handleCopy = (e) => {
    e.stopPropagation();
    if (copyValue) copyTextToClipboard(copyValue, copyLabel || label);
  };
  return (
    <div className="transaction-detail-row">
      <span className="transaction-detail-label">{label}</span>
      <span
        className={`transaction-detail-value ${muted ? 'is-muted' : ''}`}
      >
        {value}
        {copyValue && (
          <button
            type="button"
            className="transaction-detail-copy"
            onClick={handleCopy}
            aria-label={`Copy ${copyLabel || label}`}
          >
            <CopyOutlined />
          </button>
        )}
      </span>
    </div>
  );
};

const TransactionItem = ({ transaction, index }) => {
  const [isExpanded, setIsExpanded] = useState(false);

  const isSend = transaction.direction === 'send';
  const counterparty = isSend ? transaction.to : transaction.from;

  const fullDateTime = useMemo(() => {
    if (Number.isFinite(transaction.blockTimestamp)) {
      return DateTime.fromMillis(transaction.blockTimestamp).toFormat(
        'd LLL yyyy · HH:mm',
      );
    }
    return transaction.timestamp || '—';
  }, [transaction.blockTimestamp, transaction.timestamp]);

  const toggle = () => setIsExpanded((v) => !v);

  const balanceDisplay =
    transaction.runningBalance != null
      ? formatBalance(transaction.runningBalance)
      : null;

  return (
    <div
      key={index}
      className={`transaction-item ${isExpanded ? 'is-expanded' : ''}`}
      role="button"
      tabIndex={0}
      aria-expanded={isExpanded}
      onClick={toggle}
      onKeyDown={(e) => {
        // Ignore keys bubbling up from nested controls (e.g. the copy button),
        // so their Enter/Space keeps working instead of toggling the row.
        if (e.target !== e.currentTarget) return;
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggle();
        }
      }}
    >
      <div className="transaction-item-summary">
        <div className={`tx-icon ${isSend ? 'sent' : 'received'}`}>
          {isSend ? (
            <ArrowUpOutlined style={{ fontSize: 18 }} />
          ) : (
            <ArrowDownOutlined style={{ fontSize: 18 }} />
          )}
        </div>

        <div className="transaction-item-main">
          <span className="transaction-item-title">
            {isSend ? 'Sent' : 'Received'}
          </span>
          <span className="transaction-item-counterparty">
            {isSend ? 'to ' : 'from '}
            {formatWalletAddress(counterparty)}
          </span>
        </div>

        <div className="transaction-item-side">
          <span
            className={`transaction-item-amount ${
              isSend ? 'red-text' : 'green-text'
            }`}
          >
            {isSend ? '-' : '+'}
            {formatBalance(transaction.value)}
            <span className="transaction-item-ticker">{WSO2_TOKEN}</span>
          </span>
        </div>

        <DownOutlined
          className={`transaction-item-chevron ${
            isExpanded ? 'is-open' : ''
          }`}
        />
      </div>

      <div
        className="transaction-item-details"
        aria-hidden={!isExpanded}
      >
        <div className="transaction-item-divider" />
        {balanceDisplay && (
          <DetailRow
            label="Running balance"
            value={
              <>
                {balanceDisplay}
                <span className="transaction-detail-ticker">{WSO2_TOKEN}</span>
              </>
            }
          />
        )}
        <DetailRow
          label={isSend ? 'Sent to' : 'Received from'}
          value={formatWalletAddress(counterparty)}
          copyValue={counterparty}
          copyLabel={isSend ? "Recipient's address" : "Sender's address"}
        />
        {isSend && transaction.initiatedBy && (
          <DetailRow label="Sent by" value={transaction.initiatedBy} />
        )}
        <DetailRow label="When" value={fullDateTime} />
      </div>
    </div>
  );
};

export default TransactionItem;
