import { Download, FileJson, FileSpreadsheet, Link2 } from "lucide-react";
import { useState } from "react";
import type { Tariff, User } from "../../api/client";
import { Drawer } from "../../components/overlay";
import { useToast } from "../../components/toast";
import { Button, Field, Segmented } from "../../components/ui";
import { t } from "../../i18n";

type ExportFormat = "csv" | "json" | "links";
type ExportScope = "all" | "selected";

export function ExportUsersDrawer({
  open,
  onOpenChange,
  allUsers,
  selectedUsers,
  tariffs,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  allUsers: User[];
  selectedUsers: User[];
  tariffs: Map<number, Tariff>;
}) {
  const toast = useToast();
  const [format, setFormat] = useState<ExportFormat>("csv");
  const [scope, setScope] = useState<ExportScope>(selectedUsers.length > 0 ? "selected" : "all");

  const targetUsers = scope === "selected" && selectedUsers.length > 0 ? selectedUsers : allUsers;

  const handleExport = () => {
    if (!targetUsers.length) {
      toast.error("Нет пользователей для экспорта");
      return;
    }

    const dateStr = new Date().toISOString().slice(0, 10);
    let blob: Blob;
    let filename: string;

    if (format === "csv") {
      const headers = [
        "ID",
        "Имя",
        "Контакт",
        "Статус",
        "Тариф",
        "Истекает",
        "Лимит трафика (ГБ)",
        "Использовано (ГБ)",
        "Лимит устройств",
        "Ссылка на подписку",
        "Теги",
        "Заметка",
      ];

      const rows = targetUsers.map((u) => {
        const tariffName = u.tariff_id ? tariffs.get(u.tariff_id)?.name ?? "" : "";
        const limitGb = u.traffic_limit ? (u.traffic_limit / (1024 * 1024 * 1024)).toFixed(2) : "Безлимит";
        const usedGb = ((u.total_up + u.total_down) / (1024 * 1024 * 1024)).toFixed(2);
        const expires = u.expires_at ? new Date(u.expires_at).toISOString().slice(0, 19).replace("T", " ") : "Бессрочно";

        return [
          u.id,
          escapeCsv(u.name),
          escapeCsv(u.contact),
          u.state,
          escapeCsv(tariffName),
          expires,
          limitGb,
          usedGb,
          u.device_limit ?? "Безлимит",
          escapeCsv(u.sub_url),
          escapeCsv((u.tags ?? []).join("; ")),
          escapeCsv(u.note),
        ].join(",");
      });

      // \uFEFF BOM ensures Microsoft Excel displays UTF-8 Cyrillic properly
      const csvContent = "\uFEFF" + [headers.join(","), ...rows].join("\r\n");
      blob = new Blob([csvContent], { type: "text/csv;charset=utf-8;" });
      filename = `users-${dateStr}.csv`;
    } else if (format === "json") {
      const jsonContent = JSON.stringify(targetUsers, null, 2);
      blob = new Blob([jsonContent], { type: "application/json;charset=utf-8;" });
      filename = `users-${dateStr}.json`;
    } else {
      // links TXT
      const lines = targetUsers.map((u) => u.sub_url).filter(Boolean).join("\r\n");
      blob = new Blob([lines], { type: "text/plain;charset=utf-8;" });
      filename = `subscription-links-${dateStr}.txt`;
    }

    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.setAttribute("download", filename);
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);

    toast.ok(`Экспортировано ${targetUsers.length} пользователей`);
    onOpenChange(false);
  };

  return (
    <Drawer
      open={open}
      onOpenChange={onOpenChange}
      title="Экспорт пользователей"
      meta="Выгрузка базы пользователей и ссылок"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" onClick={handleExport} disabled={!targetUsers.length}>
            <Download size={18} aria-hidden /> Скачать {format.toUpperCase()}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Field label="Кого экспортировать">
          <Segmented
            label="Объём выгрузки"
            value={scope}
            onChange={(v) => setScope(v as ExportScope)}
            options={[
              { value: "all", label: `Все (${allUsers.length})` },
              {
                value: "selected",
                label: `Выбранные (${selectedUsers.length})`,
              },
            ]}
          />
        </Field>

        <Field label="Формат файла">
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <button
              type="button"
              className={`panel-soft p-3 text-left transition-colors flex flex-col gap-1 rounded-xl border ${
                format === "csv"
                  ? "border-[var(--accent)] bg-[var(--accent-glow)]"
                  : "border-transparent hover:border-[var(--edge)]"
              }`}
              onClick={() => setFormat("csv")}
            >
              <div className="flex items-center gap-2 font-medium text-[13px] text-[var(--ink-900)]">
                <FileSpreadsheet size={16} className="text-emerald-500" />
                CSV
              </div>
              <div className="text-xs text-[var(--ink-500)]">Таблица для Excel и Google Таблиц</div>
            </button>

            <button
              type="button"
              className={`panel-soft p-3 text-left transition-colors flex flex-col gap-1 rounded-xl border ${
                format === "json"
                  ? "border-[var(--accent)] bg-[var(--accent-glow)]"
                  : "border-transparent hover:border-[var(--edge)]"
              }`}
              onClick={() => setFormat("json")}
            >
              <div className="flex items-center gap-2 font-medium text-[13px] text-[var(--ink-900)]">
                <FileJson size={16} className="text-amber-500" />
                JSON
              </div>
              <div className="text-xs text-[var(--ink-500)]">Полные данные с возможностью импорта</div>
            </button>

            <button
              type="button"
              className={`panel-soft p-3 text-left transition-colors flex flex-col gap-1 rounded-xl border ${
                format === "links"
                  ? "border-[var(--accent)] bg-[var(--accent-glow)]"
                  : "border-transparent hover:border-[var(--edge)]"
              }`}
              onClick={() => setFormat("links")}
            >
              <div className="flex items-center gap-2 font-medium text-[13px] text-[var(--ink-900)]">
                <Link2 size={16} className="text-sky-500" />
                TXT ссылки
              </div>
              <div className="text-xs text-[var(--ink-500)]">Только список ссылок по одной на строку</div>
            </button>
          </div>
        </Field>

        <div className="panel-soft p-3 text-xs text-[var(--ink-500)] flex items-center justify-between">
          <span>Будет выгружено записей:</span>
          <span className="font-semibold text-[var(--ink-900)] num">{targetUsers.length}</span>
        </div>
      </div>
    </Drawer>
  );
}

function escapeCsv(val: unknown): string {
  if (val == null) return '""';
  const str = String(val);
  if (str.includes(",") || str.includes('"') || str.includes("\n") || str.includes("\r")) {
    return `"${str.replace(/"/g, '""')}"`;
  }
  return `"${str}"`;
}
