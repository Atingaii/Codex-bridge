import React from 'react';
import { FileArchive, FileText, X } from 'lucide-react';
import type { OrchestrationFile } from '../lib/types';
import { cn, formatBytes, isArchiveUpload } from '../lib/utils';

export function OrchestrationFileRow({ file, status, onRemove, removeLabel }: {
  file: OrchestrationFile;
  status?: string;
  onRemove?: () => void;
  removeLabel?: string;
}) {
  return (
    <div className={cn(
      "grid h-8 min-w-0 items-center gap-2 rounded-md border border-border bg-muted/20 px-2 text-xs",
      onRemove ? "grid-cols-[16px_minmax(0,1fr)_max-content_max-content_24px]" : "grid-cols-[16px_minmax(0,1fr)_max-content_max-content]"
    )}>
      {isArchiveUpload(file) ? <FileArchive className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
      <span className="min-w-0 truncate" title={file.name}>{file.name}</span>
      {status && <span className="whitespace-nowrap rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">{status}</span>}
      <span className="whitespace-nowrap text-[10px] text-muted-foreground">{formatBytes(file.size)}</span>
      {onRemove && (
        <button className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground" onClick={onRemove} aria-label={removeLabel || file.name}>
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}

export function OrchestrationFileList({ files, label, compact = false, status }: { files: OrchestrationFile[]; label?: string; compact?: boolean; status?: string }) {
  if (!files.length) return null;
  return (
    <div className={cn("space-y-1.5", label && "mt-2")}>
      {label && <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</div>}
      <div className={cn(compact ? "space-y-1.5" : "flex flex-wrap gap-1.5")}>
        {files.map((file, index) => (
          compact ? (
            <OrchestrationFileRow key={`${file.name}-${file.size}-${index}`} file={file} status={status} />
          ) : (
            <div key={`${file.name}-${file.size}-${index}`} className="inline-flex max-w-full min-w-0 items-center gap-2 rounded-md border border-border bg-muted/25 px-2 py-1.5 text-xs">
              {isArchiveUpload(file) ? <FileArchive className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
              <span className="min-w-0 truncate" title={file.name}>{file.name}</span>
              {status && <span className="shrink-0 rounded border border-border px-1 py-0.5 text-[10px] text-muted-foreground">{status}</span>}
              <span className="shrink-0 text-[10px] text-muted-foreground">{formatBytes(file.size)}</span>
              {file.mimeType && <span className="hidden shrink-0 rounded border border-border px-1 py-0.5 text-[10px] text-muted-foreground sm:inline">{file.mimeType}</span>}
            </div>
          )
        ))}
      </div>
    </div>
  );
}
