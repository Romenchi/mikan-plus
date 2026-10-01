import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError, errorText, setCsrf, unwrap } from "../../api/client";
import { qk } from "../../api/hooks";
import { Logo } from "../../components/atmosphere";
import { LangSwitch } from "../../components/lang";
import { Button, Field } from "../../components/ui";
import { t } from "../../i18n";

export function LoginPage() {
  const { next } = useSearch({ from: "/login" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [step, setStep] = useState<"password" | "totp">("password");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [wait, setWait] = useState(0);
  const codeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (wait <= 0) return;
    const timer = window.setTimeout(() => setWait((w) => w - 1), 1000);
    return () => window.clearTimeout(timer);
  }, [wait]);

  useEffect(() => {
    if (step === "totp") codeRef.current?.focus();
  }, [step]);

  const login = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/auth/login", { body: { username, password, totp: step === "totp" ? code : undefined } })),
    onSuccess: (me) => {
      setCsrf(me.csrf_token);
      qc.setQueryData(qk.me, me);
      const target = next && next.startsWith("/") && !next.startsWith("/login") ? next : "/";
      void navigate({ to: target });
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 401) {
        if (e.detail === "totp_required") {
          setStep("totp");
          setError("");
          return;
        }
        setError(e.detail === "invalid_totp" ? t("login.badCode") : t("login.badCredentials"));
        if (e.detail === "invalid_totp") setCode("");
        return;
      }
      if (e instanceof ApiError && e.status === 429) {
        setWait(e.retryAfter || 60);
        setError("");
        return;
      }
      setError(errorText(e));
    },
  });

  const submit = (ev: FormEvent) => {
    ev.preventDefault();
    if (wait > 0 || login.isPending) return;
    login.mutate();
  };

  return (
    <div className="grid min-h-screen place-items-center px-4 py-12">
      <LangSwitch className="fixed top-4 right-4" />
      <motion.form
        onSubmit={submit}
        className="glass-strong w-full max-w-[400px] rounded-[28px] p-8"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ type: "spring", stiffness: 300, damping: 30 }}
        noValidate
      >
        <div className="mb-8 flex items-center gap-3">
          <Logo size={40} />
          <div>
            <div className="font-display text-[22px] leading-7 font-semibold tracking-tight">mikan+</div>
            <div className="text-[13px] text-[var(--ink-500)]">{t("login.subtitle")}</div>
          </div>
        </div>

        {step === "password" ? (
          <>
            <Field label={t("login.username")} htmlFor="login-user">
              <input id="login-user" className="input" autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />
            </Field>
            <Field label={t("login.password")} htmlFor="login-pass">
              <input id="login-pass" className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </Field>
          </>
        ) : (
          <>
            <div className="panel-soft mb-4 flex gap-3 p-3 text-[13px] text-[var(--ink-600)]">
              <ShieldCheck size={18} className="mt-0.5 shrink-0 text-[var(--leaf-500)]" aria-hidden />
              {t("login.totpHint")}
            </div>
            <Field label={t("login.code")} htmlFor="login-code">
              <input
                id="login-code"
                ref={codeRef}
                className="input mono text-center text-lg tracking-[0.3em]"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={16}
                value={code}
                onChange={(e) => setCode(e.target.value.trim())}
              />
            </Field>
          </>
        )}

        {error ? (
          <p className="mb-4 text-[13px] text-[var(--berry-600)]" role="alert">
            {error}
          </p>
        ) : null}
        {wait > 0 ? (
          <p className="mb-4 text-[13px] text-[var(--honey-600)]" role="alert">
            {t("login.tooMany", { s: wait })}
          </p>
        ) : null}

        <Button type="submit" variant="primary" block loading={login.isPending} disabled={wait > 0 || !username || !password || (step === "totp" && code.length < 6)}>
          {step === "password" ? t("login.submit") : t("login.confirm")}
        </Button>
        {step === "totp" ? (
          <button
            type="button"
            className="link-btn mt-4 block w-full text-center text-[13px]"
            onClick={() => {
              setStep("password");
              setCode("");
              setError("");
            }}
          >
            {t("login.back")}
          </button>
        ) : (
          <p className="mt-6 text-center text-xs text-[var(--ink-500)]">
            {t("login.forgot")} <span className="mono">mikan reset-password</span>
          </p>
        )}
      </motion.form>
    </div>
  );
}
