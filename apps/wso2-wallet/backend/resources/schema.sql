-- Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
--
-- WSO2 LLC. licenses this file to you under the Apache License,
-- Version 2.0 (the "License"); you may not use this file except
-- in compliance with the License.
-- You may obtain a copy of the License at
--
-- http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing,
-- software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
-- KIND, either express or implied.  See the License for the
-- specific language governing permissions and limitations
-- under the License.

-- Wallet ledger schema. Run against the wallet database.

CREATE TABLE IF NOT EXISTS user_wallet (
    wallet_address          VARCHAR(255) NOT NULL PRIMARY KEY,
    user_email              VARCHAR(255) NOT NULL,
    default_wallet          BOOLEAN NOT NULL DEFAULT FALSE,
    initial_coins_allocated DECIMAL(20, 10) NOT NULL DEFAULT 0,
    total_balance           VARCHAR(255) NULL COMMENT 'Encrypted current balance',
    wallet_name             VARCHAR(512) NULL COMMENT 'Encrypted display name; shown only while the wallet is shared',
    created_on              TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_user_email (user_email)
);

CREATE TABLE IF NOT EXISTS `transaction` (
    id                 BIGINT AUTO_INCREMENT PRIMARY KEY,
    from_address       VARCHAR(255) NOT NULL,
    to_address         VARCHAR(255) NOT NULL,
    amount             VARCHAR(255) NOT NULL COMMENT 'Encrypted transferred amount',
    reference          VARCHAR(66) NULL COMMENT 'Unique transaction reference',
    source             VARCHAR(32) NULL COMMENT 'Originating channel/app',
    initiated_by       VARCHAR(512) NULL COMMENT 'Encrypted email of the user who initiated the transfer',
    created_on         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    tx_type            VARCHAR(20) NULL COMMENT 'Optional source metadata',
    tx_hash            VARCHAR(66) NULL COMMENT 'Optional source metadata',
    tx_log_index       INT NULL COMMENT 'Optional source metadata',
    tx_block_number    BIGINT NULL COMMENT 'Optional source metadata',
    tx_block_timestamp DATETIME NULL COMMENT 'Optional source timestamp',
    UNIQUE KEY uq_reference (reference),
    UNIQUE KEY uq_tx_log (tx_hash, tx_log_index),
    INDEX idx_from (from_address),
    INDEX idx_to (to_address)
);

-- People other than the owner who may use a wallet. The owner stays in
-- user_wallet.user_email and never has a row here.
CREATE TABLE IF NOT EXISTS wallet_access (
    wallet_address VARCHAR(255) NOT NULL,
    user_email     VARCHAR(255) NOT NULL,
    access_level   VARCHAR(16)  NOT NULL COMMENT 'READ or TRANSACT',
    seal           VARCHAR(255) NOT NULL COMMENT 'Encrypted access level bound to the wallet and user; rows that fail it are ignored',
    granted_by     VARCHAR(255) NOT NULL,
    created_on     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (wallet_address, user_email),
    INDEX idx_access_email (user_email)
);
