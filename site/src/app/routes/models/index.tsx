import { Link, createFileRoute } from '@tanstack/react-router'

import { copy } from '@/copy'
import { formatYuan } from '@/lib/money'
import {
  Multiplier,
  PageHeader,
  PeakBadge,
  Price,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  Tag,
} from '@/ui'

export const Route = createFileRoute('/models/')({
  component: ModelsPage,
})

// Demo data only: the real square arrives from GET /api/marketplace/square/models.
const SAMPLE_NEXT_SWITCH = Math.floor(Date.now() / 1000) + 2 * 3600 + 18 * 60

function ModelsPage() {
  const models = copy.pages.models

  return (
    <div className="mx-auto max-w-[1180px] px-5 py-10">
      <PageHeader
        title={models.title}
        lead={models.lead}
        actions={<Tag tone="brand">{copy.common.sample}</Tag>}
      />
      <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-muted">
        <span>{models.priceUnit}</span>
        <span>{models.filtersSoon}</span>
      </div>

      <p className="mt-3 text-[12px] text-muted">{copy.common.sampleNotice}</p>

      <div className="mt-5">
        <Table minWidth={980}>
          <TableHead>
            <TableRow interactive={false}>
              <TableHeaderCell>{models.columns.model}</TableHeaderCell>
              <TableHeaderCell>{models.columns.vendor}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.official}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.multiplier}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.buyerPrice}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.sellers}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.availability}</TableHeaderCell>
              <TableHeaderCell numeric>{models.columns.latency}</TableHeaderCell>
              <TableHeaderCell>{models.columns.tier}</TableHeaderCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {models.sample.map((row) => (
              <TableRow key={row.id} interactive>
                <TableCell>
                  <Link
                    to="/models/$id"
                    params={{ id: row.id }}
                    className="font-medium text-ink transition-colors hover:text-brand-dark"
                  >
                    {row.name}
                  </Link>
                  <div className="font-numeric text-[11px] text-muted">{row.id}</div>
                </TableCell>
                <TableCell className="text-muted">{row.vendor}</TableCell>
                <TableCell numeric>
                  <div className="leading-tight">
                    <div>{formatYuan(row.officialInput)}</div>
                    <div className="text-[11px] text-muted">{formatYuan(row.officialOutput)}</div>
                  </div>
                </TableCell>
                <TableCell numeric>
                  <Multiplier milli={row.multiplierMilli} />
                </TableCell>
                <TableCell numeric>
                  <Price value={row.buyerInput} tier={row.tier} size="sm" unit={null} />
                  <div className="text-[11px] text-muted">
                    {models.outputPrefix}{' '}
                    <span className="font-numeric tabular-nums">{formatYuan(row.buyerOutput)}</span>
                  </div>
                </TableCell>
                <TableCell numeric>
                  {row.sellers} {models.sellerUnit}
                </TableCell>
                <TableCell numeric>{row.availability}</TableCell>
                <TableCell numeric className="text-muted">
                  {row.latency}
                </TableCell>
                <TableCell>
                  <PeakBadge tier={row.tier} nextSwitchAt={SAMPLE_NEXT_SWITCH} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
