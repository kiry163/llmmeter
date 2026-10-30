import type { ReactElement, ReactNode } from "react";

export interface Column<T> {
  key: string;
  header: ReactNode;
  /** className is applied to both the header cell and the body cells. */
  className?: string;
  render: (row: T) => ReactNode;
}

export interface DataTableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  onRowClick?: (row: T) => void;
  empty?: ReactNode;
}

/**
 * DataTable renders the shared table shell: sticky header, clickable rows and
 * an empty slot. Alignment and sizing stay with the caller's classNames.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  empty,
}: DataTableProps<T>): ReactElement {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead className="sticky top-0 z-10 bg-surface-raised text-xs text-muted">
          <tr className="border-b border-line">
            {columns.map((column) => (
              <th
                key={column.key}
                scope="col"
                className={`whitespace-nowrap py-2 pr-3 font-medium ${column.className ?? ""}`}
              >
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={rowKey(row)}
              tabIndex={onRowClick ? 0 : undefined}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              onKeyDown={
                onRowClick
                  ? (event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onRowClick(row);
                      }
                    }
                  : undefined
              }
              className={`h-14 border-b border-line/60 ${
                onRowClick
                  ? "cursor-pointer hover:bg-line/20 focus:bg-line/20 focus:outline-none"
                  : ""
              }`}
            >
              {columns.map((column) => (
                <td key={column.key} className={`py-2 pr-3 ${column.className ?? ""}`}>
                  {column.render(row)}
                </td>
              ))}
            </tr>
          ))}
          {rows.length === 0 && (
            <tr>
              <td colSpan={Math.max(columns.length, 1)} className="py-8 text-center text-muted">
                {empty ?? "暂无数据"}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
