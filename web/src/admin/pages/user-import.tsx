import { useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { AlertCircle, Check, Upload } from "lucide-react";
import { useEffect, useMemo, useState, type ChangeEvent } from "react";
import { api, type Tariff } from "../../api/client";
import { qk } from "../../api/hooks";
import { Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, Field } from "../../components/ui";
import { t } from "../../i18n";
import { tariffSummary } from "./tariffs";

interface ParsedUser {
  name: string;
  contact?: string;
  note?: string;
  tags?: string[];
  tariff_id?: number;
}

export function ImportUsersDrawer({
  open,
  onOpenChange,
  tariffs,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  tariffs: Tariff[];
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [rawText, setRawText] = useState("");
  const [fallbackTariffId, setFallbackTariffId] = useState<number>();
  const [importing, setImporting] = useState(false);
  const [progress, setProgress] = useState<{ current: number; total: number } | null>(null);
  const [errorMsg, setErrorMsg] = useState("");

  // Select fallback tariff default
  useEffect(() => {
    if (fallbackTariffId === undefined && tariffs.length) {
      setFallbackTariffId(tariffs[Math.min(1, tariffs.length - 1)]!.id);
    }
  }, [tariffs, fallbackTariffId]);

  useEffect(() => {
    if (!open) {
      setRawText("");
      setProgress(null);
      setErrorMsg("");
      setImporting(false);
    }
  }, [open]);

  const handleFileUpload = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const content = event.target?.result as string;
      if (content) {
        setRawText(content);
        setErrorMsg("");
      }
    };
    reader.readAsText(file, "UTF-8");
  };

  const parsedUsers = useMemo<ParsedUser[]>(() => {
    const text = rawText.trim();
    if (!text) return [];

    // Try parsing as JSON first
    if (text.startsWith("[") || text.startsWith("{")) {
      try {
        const json = JSON.parse(text);
        const list = Array.isArray(json) ? json : json.users || json.items || [];
        if (Array.isArray(list)) {
          return list
            .filter((item): item is Record<string, unknown> => !!item && typeof item === "object")
            .map((item) => ({
              name: String(item.name || item.username || item.id || "Пользователь").trim(),
              contact: item.contact ? String(item.contact).trim() : undefined,
              note: item.note ? String(item.note).trim() : undefined,
              tags: Array.isArray(item.tags) ? item.tags.map(String) : undefined,
              tariff_id: typeof item.tariff_id === "number" ? item.tariff_id : undefined,
            }))
            .filter((u) => u.name.length > 0);
        }
      } catch {
        // Not valid JSON, try CSV below
      }
    }

    // Try parsing as CSV / TSV / Semicolon-delimited
    const lines = text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
    if (!lines.length) return [];

    // Detect delimiter: comma, semicolon or tab
    const firstLine = lines[0] ?? "";
    let delimiter = ",";
    if (firstLine.includes(";") && (firstLine.match(/;/g)?.length ?? 0) >= (firstLine.match(/,/g)?.length ?? 0)) {
      delimiter = ";";
    } else if (firstLine.includes("\t")) {
      delimiter = "\t";
    }

    const rows = lines.map((line) => parseCsvLine(line, delimiter));
    if (!rows.length || !rows[0]) return [];

    const firstRowLower = rows[0].map((c) => c.toLowerCase().trim());
    let startIndex = 0;
    let nameCol = 0;
    let contactCol = 1;
    let noteCol = -1;
    let tagsCol = -1;
    let tariffCol = -1;

    // Detect header row
    const hasHeader = firstRowLower.some((c) => c === "name" || c === "имя" || c === "contact" || c === "контакт");
    if (hasHeader) {
      startIndex = 1;
      firstRowLower.forEach((col, idx) => {
        if (col === "name" || col === "имя") nameCol = idx;
        else if (col === "contact" || col === "контакт" || col === "email" || col === "telegram") contactCol = idx;
        else if (col === "note" || col === "заметка") noteCol = idx;
        else if (col === "tags" || col === "теги") tagsCol = idx;
        else if (col === "tariff" || col === "тариф" || col === "tariff_id") tariffCol = idx;
      });
    }

    const users: ParsedUser[] = [];
    for (let i = startIndex; i < rows.length; i++) {
      const r = rows[i];
      if (!r) continue;
      const name = (r[nameCol] || "").trim();
      if (!name) continue;

      const contactVal = contactCol >= 0 ? r[contactCol] : undefined;
      const contact = contactVal ? contactVal.trim() : undefined;

      const noteVal = noteCol >= 0 ? r[noteCol] : undefined;
      const note = noteVal ? noteVal.trim() : undefined;

      const tagsVal = tagsCol >= 0 ? r[tagsCol] : undefined;
      const tags = tagsVal ? tagsVal.split(/[;,]/).map((t) => t.trim()).filter(Boolean) : undefined;

      const tariffRaw = tariffCol >= 0 ? r[tariffCol] : undefined;
      const tariffVal = tariffRaw ? parseInt(tariffRaw, 10) : undefined;

      users.push({
        name,
        contact: contact || undefined,
        note: note || undefined,
        tags: tags?.length ? tags : undefined,
        tariff_id: tariffVal !== undefined && !isNaN(tariffVal) ? tariffVal : undefined,
      });
    }

    return users;
  }, [rawText]);

  const handleImport = async () => {
    if (!parsedUsers.length) {
      setErrorMsg("Нет пользователей для импорта. Проверьте формат текста или файла.");
      return;
    }
    if (!fallbackTariffId) {
      setErrorMsg("Выберите тариф по умолчанию.");
      return;
    }

    setImporting(true);
    setProgress({ current: 0, total: parsedUsers.length });
    setErrorMsg("");

    let successCount = 0;
    let failCount = 0;

    for (let i = 0; i < parsedUsers.length; i++) {
      const u = parsedUsers[i];
      if (!u) continue;
      const tariffId = u.tariff_id && tariffs.some((t) => t.id === u.tariff_id) ? u.tariff_id : fallbackTariffId;

      try {
        await api.POST("/api/v1/users", {
          body: {
            name: u.name,
            contact: u.contact,
            note: u.note,
            tags: u.tags,
            tariff_id: tariffId,
          },
        });
        successCount++;
      } catch (err) {
        console.error("Failed to import user:", u.name, err);
        failCount++;
      }

      setProgress({ current: i + 1, total: parsedUsers.length });
    }

    setImporting(false);
    void qc.invalidateQueries({ queryKey: qk.users });

    if (failCount === 0) {
      toast.ok(`Успешно импортировано пользователей: ${successCount}`);
      onOpenChange(false);
    } else {
      toast.error(`Импортировано: ${successCount}, с ошибкой: ${failCount}`);
      onOpenChange(false);
    }
  };

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title="Импорт пользователей"
      meta="Массовое создание пользователей из CSV или JSON"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={importing}>
            {t("common.cancel")}
          </Button>
          <Button
            variant="primary"
            onClick={handleImport}
            disabled={!parsedUsers.length || importing || !fallbackTariffId}
            loading={importing}
          >
            <Check size={18} aria-hidden />{" "}
            {importing
              ? `Импорт ${progress?.current ?? 0}/${progress?.total ?? 0}`
              : `Импортировать (${parsedUsers.length})`}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5 pt-3">
        <Field label="Загрузить файл (.csv, .json, .txt)">
          <div className="flex items-center gap-3">
            <label className="btn btn-glass cursor-pointer">
              <Upload size={16} aria-hidden />
              <span>Выбрать файл</span>
              <input
                type="file"
                className="hidden"
                accept=".csv,.json,.txt,text/csv,application/json,text/plain"
                onChange={handleFileUpload}
              />
            </label>
            <span className="text-xs text-[var(--ink-500)]">или вставьте данные ниже</span>
          </div>
        </Field>

        <Field
          label="Данные пользователей"
          hint="Поддерживаются CSV (Имя,Контакт,Заметка), TSV и JSON"
        >
          <textarea
            className="input mono text-xs"
            rows={6}
            placeholder={`Имя,Контакт,Заметка\nИван Иванов,@ivan,Тестовый клиент\nПетр Петров,+79991234567,Друг`}
            value={rawText}
            onChange={(e) => {
              setRawText(e.target.value);
              setErrorMsg("");
            }}
          />
        </Field>

        <Field label="Тариф по умолчанию" hint="Будет применен, если в строке не указан тариф">
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup">
            {tariffs.map((tr) => (
              <button
                key={tr.id}
                type="button"
                role="radio"
                aria-checked={fallbackTariffId === tr.id}
                className={clsx("opt")}
                onClick={() => setFallbackTariffId(tr.id)}
              >
                <span className="font-semibold">{tr.name}</span>
                <span className="text-xs text-[var(--ink-500)]">{tariffSummary(tr)}</span>
                {tr.price_label ? (
                  <span className="mt-1 text-[13px] font-medium text-[var(--mikan-700)]">
                    {tr.price_label}
                  </span>
                ) : null}
              </button>
            ))}
          </div>
        </Field>

        {errorMsg ? (
          <div className="panel-soft p-3 text-xs text-[var(--berry-600)] flex items-center gap-2">
            <AlertCircle size={16} className="shrink-0" />
            <span>{errorMsg}</span>
          </div>
        ) : null}

        {parsedUsers.length > 0 ? (
          <div className="panel-soft p-3 flex flex-col gap-2 rounded-xl">
            <div className="flex items-center justify-between text-xs font-semibold text-[var(--ink-900)]">
              <span>Готовы к импорту:</span>
              <span className="num font-bold text-[var(--accent)]">{parsedUsers.length} пользователей</span>
            </div>
            <div className="max-h-36 overflow-y-auto flex flex-col gap-1 text-xs text-[var(--ink-600)]">
              {parsedUsers.slice(0, 5).map((u, i) => (
                <div key={i} className="truncate border-b border-[var(--edge)] pb-1 last:border-0">
                  <span className="font-medium text-[var(--ink-800)]">{u.name}</span>
                  {u.contact ? ` · ${u.contact}` : ""}
                  {u.note ? ` (${u.note})` : ""}
                </div>
              ))}
              {parsedUsers.length > 5 ? (
                <div className="text-[var(--ink-400)] italic">
                  ...и ещё {parsedUsers.length - 5} записей
                </div>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>
    </Drawer>
  );
}

function parseCsvLine(text: string, delimiter: string): string[] {
  const result: string[] = [];
  let cur = "";
  let inQuotes = false;

  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      if (inQuotes && text[i + 1] === '"') {
        cur += '"';
        i++;
      } else {
        inQuotes = !inQuotes;
      }
    } else if (c === delimiter && !inQuotes) {
      result.push(cur.trim());
      cur = "";
    } else {
      cur += c;
    }
  }
  result.push(cur.trim());
  return result;
}
