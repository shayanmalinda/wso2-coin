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

// Package model holds the API request and response types.
package model

// Direction classifies a transaction relative to the wallet viewing it.
type Direction string

const (
	// DirectionSent means the wallet is the sender.
	DirectionSent Direction = "sent"
	// DirectionReceived means the wallet is the recipient.
	DirectionReceived Direction = "received"
)

// Role is what the caller may do with a wallet.
type Role string

const (
	// RoleOwner may view, transfer, rename and manage access.
	RoleOwner Role = "OWNER"
	// RoleTransact may view and transfer.
	RoleTransact Role = "TRANSACT"
	// RoleRead may only view the balance and history.
	RoleRead Role = "READ"
)

// Wallet is a wallet the caller owns or has been granted access to.
type Wallet struct {
	Address       string `json:"address"`
	DefaultWallet bool   `json:"defaultWallet"`
	CreatedOn     string `json:"createdOn"`
	// Name is set only while the wallet is shared with at least one other person.
	Name string `json:"name,omitempty"`
	Role Role   `json:"role"`
	// Shared reports whether anyone other than the owner has access.
	Shared bool `json:"shared"`
}

// WalletInfo is the public description of a wallet, shown to someone sending to it.
type WalletInfo struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

// RenameRequest is the body of a wallet rename. An empty name clears it.
type RenameRequest struct {
	// Name is a pointer so a missing or null name can be told apart from an empty one.
	Name *string `json:"name"`
}

// AccessGrant is one person's access to a shared wallet.
type AccessGrant struct {
	Email       string `json:"email"`
	AccessLevel Role   `json:"accessLevel"`
	GrantedBy   string `json:"grantedBy"`
	GrantedOn   string `json:"grantedOn"`
}

// AccessRequest is the body of a grant: who gets access and at what level.
type AccessRequest struct {
	Email       string `json:"email"`
	AccessLevel Role   `json:"accessLevel"`
}

// RevokeRequest is the body of a revoke: whose access to remove.
type RevokeRequest struct {
	Email string `json:"email"`
}

// Balance is a wallet's current balance.
type Balance struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
}

// Transaction is one entry in a wallet's history.
type Transaction struct {
	TransactionHash string    `json:"transactionHash"`
	FromAddress     string    `json:"fromAddress"`
	ToAddress       string    `json:"toAddress"`
	Amount          string    `json:"amount"`
	Direction       Direction `json:"direction"`
	Timestamp       string    `json:"timestamp"`
	// InitiatedBy is who made the transfer; set only on transfers sent from the wallet.
	InitiatedBy string `json:"initiatedBy,omitempty"`
}

// TransactionPage is a paginated slice of transactions.
type TransactionPage struct {
	Transactions []Transaction `json:"transactions"`
	Limit        int           `json:"limit"`
	Offset       int           `json:"offset"`
	HasMore      bool          `json:"hasMore"`
}

// AppConfig exposes client-facing runtime configuration.
type AppConfig struct {
	MaintenanceMode    bool   `json:"maintenanceMode"`
	MaintenanceMessage string `json:"maintenanceMessage"`
}

// TransferRequest is the body of a transfer.
type TransferRequest struct {
	FromAddress string `json:"fromAddress"`
	ToAddress   string `json:"toAddress"`
	Amount      string `json:"amount"`
}

// TransferResponse is returned after a successful transfer.
type TransferResponse struct {
	TransactionHash string `json:"transactionHash"`
}

// WalletHealth reports service and wallet integrity for a wallet.
type WalletHealth struct {
	Healthy       bool `json:"healthy"`
	Exists        bool `json:"exists"`
	BalanceIntact bool `json:"balanceIntact"`
}
