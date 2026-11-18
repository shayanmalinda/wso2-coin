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

-- Shared, named wallets. Run once against an existing wallet database.
-- Every change is additive (nullable columns and one new table), so it can be
-- applied ahead of the backend deployment.

ALTER TABLE user_wallet
    ADD COLUMN wallet_name VARCHAR(512) NULL
        COMMENT 'Encrypted display name; shown only while the wallet is shared';

ALTER TABLE `transaction`
    ADD COLUMN initiated_by VARCHAR(512) NULL
        COMMENT 'Encrypted email of the user who initiated the transfer';

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
