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

import React, { useState, useEffect, useRef } from "react";
import { Avatar } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowRightOutlined,
  LoadingOutlined,
  SendOutlined,
  WarningFilled,
} from "@ant-design/icons";
import Wso2MainImg from "../../assets/images/pulse-orange.png";
import { useLocation, useNavigate } from "react-router-dom";
import "./ConfirmSendAssets.css";
import { getLocalDataAsync, saveLocalDataAsync } from "../../helpers/storage";
import { getWalletName, transferToken } from "../../services/wallet.service";
import { getEllipsisTxt } from "../../helpers/formatter";
import {
  ERROR,
  ERROR_FETCHING_LOCAL_TX_DETAILS,
  ERROR_RESETTING_TX_VALUES,
  ERROR_TRANSFERRING_TOKEN,
  ERROR_BRIDGE_NOT_READY,
  OK,
  SUCCESS,
  SUCCESS_TOKEN_TRANSFER,
  WSO2_TOKEN,
} from "../../constants/strings";
import { STORAGE_KEYS } from "../../constants/configs";
import { showToast, showAlertBox } from "../../helpers/alerts";
import { waitForBridge } from "../../helpers/bridge";
import { completePayment } from "../../helpers/paymentFlow";
import { requestOpenMicroApp } from "../../microapp-bridge";

function ConfirmSendAssets() {
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();

  const [fromAddress, setFromAddress] = useState("");
  const [sendAmount, setSendAmount] = useState("");
  const [senderAddress, setSenderAddress] = useState("");
  // Name of the recipient wallet, when it is a named shared wallet.
  const [recipientName, setRecipientName] = useState("");
  const [isTransferLoading, setIsTransferLoading] = useState(false);
  const [paymentFlowData, setPaymentFlowData] = useState(null);
  // Synchronous guard against double submission (state updates lag a rapid re-tap).
  const isSubmittingRef = useRef(false);

  const fetchLocalTxDetails = async () => {
    try {
      const sendingAmount = await getLocalDataAsync(
        STORAGE_KEYS.SENDING_AMOUNT,
      );
      const senderWalletAddress = await getLocalDataAsync(
        STORAGE_KEYS.SENDER_WALLET_ADDRESS,
      );
      const walletAddress = await getLocalDataAsync(
        STORAGE_KEYS.WALLET_ADDRESS,
      );

      setSendAmount(sendingAmount);
      setSenderAddress(senderWalletAddress);
      setFromAddress(walletAddress);
    } catch (error) {
      console.log(`${ERROR_FETCHING_LOCAL_TX_DETAILS}: ${error}`);
    }
  };

  useEffect(() => {
    const isParking = location?.state?.isParkingPaymentFlow;
    const isShop = location?.state?.isShopPaymentFlow;

    // Check if redirect originated from the Parking app or the Shop app
    // and store the routing details so we can redirect back later.
    if (isParking || isShop) {
      setPaymentFlowData({
        flow: isParking ? "PARKING" : "SHOP",
        returnAppId: location?.state?.returnAppId || "",
        returnRoute: location?.state?.returnRoute || "",
      });
    }
  }, [location]);

  useEffect(() => {
    fetchLocalTxDetails();
  }, []);

  useEffect(() => {
    if (!senderAddress) return undefined;
    let cancelled = false;
    getWalletName(senderAddress).then((name) => {
      if (!cancelled) setRecipientName(name);
    });
    return () => {
      cancelled = true;
    };
  }, [senderAddress]);

  const resetInputFields = async () => {
    try {
      await saveLocalDataAsync(STORAGE_KEYS.SENDING_AMOUNT, "");
      await saveLocalDataAsync(STORAGE_KEYS.SENDER_WALLET_ADDRESS, "");
    } catch (error) {
      console.log(`${ERROR_RESETTING_TX_VALUES}: ${error}`);
    }
  };

  const handleReject = async () => {
    await resetInputFields();
    // If the payment is rejected, write the FAILED status to the respective keys
    // (parking or shop) and return back to the calling microapp.
    if (paymentFlowData) {
      await completePayment({
        flow: paymentFlowData.flow,
        status: "FAILED",
        error: "User cancelled payment",
        saveLocalDataAsync,
        requestOpenMicroApp,
        returnAppId: paymentFlowData.returnAppId,
        returnRoute: paymentFlowData.returnRoute,
      });
      return;
    }
    navigate("/send");
  };

  const handleConfirm = async () => {
    if (isSubmittingRef.current) return;
    isSubmittingRef.current = true;

    let receipt = null;
    let transferFailed = false;

    try {
      const isBridgeReady = await waitForBridge();
      if (!isBridgeReady) {
        console.error(ERROR_BRIDGE_NOT_READY);
        showAlertBox(ERROR, ERROR_BRIDGE_NOT_READY, OK);
        isSubmittingRef.current = false;
        return;
      }

      setIsTransferLoading(true);
      receipt = await transferToken(senderAddress, sendAmount);
    } catch (error) {
      console.log("error while transferring token", error);
      transferFailed = true;

      // On transaction errors:
      // Report FAILED status back to the calling microapp.
      if (paymentFlowData) {
        try {
          await completePayment({
            flow: paymentFlowData.flow,
            status: "FAILED",
            error: ERROR_TRANSFERRING_TOKEN,
            saveLocalDataAsync,
            requestOpenMicroApp,
            returnAppId: paymentFlowData.returnAppId,
            returnRoute: paymentFlowData.returnRoute,
          });
        } catch (flowError) {
          console.log(
            "error while reporting payment failure",
            flowError,
          );
        }
      }

      showAlertBox(ERROR, ERROR_TRANSFERRING_TOKEN, OK);
      setIsTransferLoading(false);
      isSubmittingRef.current = false;
    }

    if (receipt) {
      try {
        await resetInputFields();

        if (fromAddress) {
          const cachedBalance = queryClient.getQueryData([
            "walletBalance",
            fromAddress,
          ]);
          const balanceNum = Number(cachedBalance);
          const amountNum = Number(sendAmount);
          if (
            cachedBalance != null &&
            Number.isFinite(balanceNum) &&
            Number.isFinite(amountNum)
          ) {
            const optimistic = Math.max(0, balanceNum - amountNum).toString();
            queryClient.setQueryData(
              ["walletBalance", fromAddress],
              optimistic,
            );
          }

          // Refresh balances and history for every wallet: the sender is debited
          // and the recipient (which may be another of the user's own wallets) is
          // credited, so a single-wallet invalidation would leave it stale.
          queryClient.invalidateQueries({ queryKey: ["walletBalance"] });
          queryClient.invalidateQueries({ queryKey: ["transactions"] });
        }
      } catch (stateError) {
        console.error("Error resetting fields or updating query cache", stateError);
      }

      // On successful payment confirmation:
      // Write the SUCCESS status and transaction hash back to the respective keys.
      if (paymentFlowData) {
        try {
          await completePayment({
            flow: paymentFlowData.flow,
            status: "SUCCESS",
            txHash: receipt?.transactionHash || "",
            saveLocalDataAsync,
            requestOpenMicroApp,
            returnAppId: paymentFlowData.returnAppId,
            returnRoute: paymentFlowData.returnRoute,
          });
        } catch (paymentError) {
          console.error("Error completing payment via bridge", paymentError);
        }
        setIsTransferLoading(false);
        return;
      }

      showToast(SUCCESS, SUCCESS_TOKEN_TRANSFER);
      setIsTransferLoading(false);
      setTimeout(() => {
        navigate("/");
      }, 500);
    } else if (!transferFailed) {
      setIsTransferLoading(false);
      isSubmittingRef.current = false;
    }
  };

  const isParkingFlow = paymentFlowData?.flow === "PARKING";

  return (
    <div className="confirm-page">
      <div className="confirm-hero">
        <div className="confirm-hero-label">You're sending</div>
        <div className="confirm-hero-amount">{sendAmount || "—"}</div>
        <div className="confirm-hero-chip">
          <Avatar size={22} src={Wso2MainImg} />
          <span className="confirm-hero-chip-text">{WSO2_TOKEN}</span>
        </div>
      </div>

      <div className="confirm-card">
        <div className="confirm-from-to">
          <div className="confirm-addr-blk">
            <div className="confirm-addr-lbl">From</div>
            <div className="confirm-addr-val">
              {fromAddress ? getEllipsisTxt(fromAddress, 6) : "—"}
            </div>
          </div>
          <div className="confirm-arrow-sep">
            <ArrowRightOutlined style={{ fontSize: 12, color: "#9CA3AF" }} />
          </div>
          <div className="confirm-addr-blk confirm-addr-blk-right">
            <div className="confirm-addr-lbl">To</div>
            {recipientName && (
              <div className="confirm-addr-name">{recipientName}</div>
            )}
            <div className="confirm-addr-val">
              {senderAddress ? getEllipsisTxt(senderAddress, 6) : "—"}
            </div>
          </div>
        </div>

        <div className="confirm-total-row">
          <span className="confirm-total-lbl">Total</span>
          <div className="confirm-total-val">
            <span className="confirm-total-num">{sendAmount || "—"}</span>
            <div className="confirm-ticker-pill">
              <Avatar size={20} src={Wso2MainImg} />
              <span className="confirm-ticker-text">{WSO2_TOKEN}</span>
            </div>
          </div>
        </div>
      </div>

      <div className="confirm-warn">
        <WarningFilled style={{ color: "#F97316", fontSize: 16, flexShrink: 0 }} />
        <span className="confirm-warn-text">
          Crypto transactions are irreversible. Double-check the recipient
          address before confirming.
        </span>
      </div>

      <div className="confirm-actions">
        {!isParkingFlow && (
          <button
            type="button"
            className="confirm-cancel-btn"
            onClick={handleReject}
            disabled={isTransferLoading}
          >
            Cancel
          </button>
        )}
        <button
          type="button"
          className={`confirm-primary-btn ${
            isParkingFlow ? "confirm-primary-btn-full" : ""
          }`}
          onClick={handleConfirm}
          disabled={isTransferLoading || !sendAmount || !senderAddress}
        >
          {isTransferLoading ? (
            <LoadingOutlined style={{ fontSize: 16 }} spin />
          ) : (
            <SendOutlined style={{ fontSize: 14 }} />
          )}
          <span>{isTransferLoading ? "Sending..." : "Confirm & Send"}</span>
        </button>
      </div>
    </div>
  );
}

export default ConfirmSendAssets;
