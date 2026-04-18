"use client";

import { useEffect, useRef } from "react";
import type { CandleDoc } from "../types";

export function CandleChart({ candles }: { candles: CandleDoc[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || candles.length === 0) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const W = canvas.width;
    const H = canvas.height;
    if (W === 0 || H === 0) return;

    const pad = { top: 20, bottom: 30, left: 10, right: 10 };
    const chartW = W - pad.left - pad.right;
    const chartH = H - pad.top - pad.bottom;

    const recent = candles.slice(-120).filter((c) => c.close > 0);
    if (recent.length === 0) return;

    const highs = recent.map((c) => (c.high > 0 ? c.high : c.close));
    const lows = recent.map((c) => (c.low > 0 ? c.low : c.close));
    const maxP = highs.reduce((a, b) => Math.max(a, b), highs[0]);
    const minP = lows.reduce((a, b) => Math.min(a, b), lows[0]);
    const range = maxP - minP || 1;

    const toY = (p: number) => pad.top + chartH - ((p - minP) / range) * chartH;
    const barW = Math.max(1, Math.floor(chartW / recent.length) - 1);

    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = "#0f1117";
    ctx.fillRect(0, 0, W, H);

    ctx.strokeStyle = "#1e2535";
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const y = pad.top + (chartH / 4) * i;
      ctx.beginPath();
      ctx.moveTo(pad.left, y);
      ctx.lineTo(W - pad.right, y);
      ctx.stroke();
    }

    recent.forEach((c, i) => {
      const x = pad.left + (i / recent.length) * chartW;
      const open = c.open > 0 ? c.open : c.close;
      const close = c.close;
      const high = c.high > 0 ? c.high : Math.max(open, close);
      const low = c.low > 0 ? c.low : Math.min(open, close);
      const isUp = close >= open;

      ctx.strokeStyle = isUp ? "#34d399" : "#f87171";
      ctx.fillStyle = isUp ? "#34d399" : "#f87171";
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(x + barW / 2, toY(high));
      ctx.lineTo(x + barW / 2, toY(low));
      ctx.stroke();

      const bodyTop = toY(Math.max(open, close));
      const bodyH = Math.max(1, Math.abs(toY(open) - toY(close)));
      ctx.fillRect(x, bodyTop, barW, bodyH);
    });

    ctx.fillStyle = "#94a3b8";
    ctx.font = "11px monospace";
    ctx.fillText(`$${recent[recent.length - 1]?.close.toFixed(2)}`, pad.left + 4, pad.top - 4);
  }, [candles]);

  return (
    <canvas
      ref={canvasRef}
      width={800}
      height={300}
      className="w-full rounded-lg"
      style={{ imageRendering: "pixelated" }}
    />
  );
}
