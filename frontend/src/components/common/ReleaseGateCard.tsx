import { BlockRounded, CheckCircleRounded, WarningAmberRounded } from '@mui/icons-material'
import { Alert, Box, Chip, Typography } from '@mui/material'
import { releaseGateLabels } from '../../types/enums/release-gate'
import type { ReleaseGate } from '../../types/rollover-scenario'
import { formatDateTime } from '../../utils/date'

interface ReleaseGateCardProps {
  gate: ReleaseGate
  simulated: boolean
  riskAcceptance?: string
}

const tone: Record<ReleaseGate['decision'], { className: string; icon: JSX.Element; severity: 'success' | 'warning' | 'error' }> = {
  pass: { className: 'is-pass', icon: <CheckCircleRounded />, severity: 'success' },
  risk_acceptance_required: { className: 'is-risk', icon: <WarningAmberRounded />, severity: 'warning' },
  blocked_critical: { className: 'is-block', icon: <BlockRounded />, severity: 'error' },
}

export function ReleaseGateCard({ gate, simulated, riskAcceptance }: ReleaseGateCardProps) {
  const present = tone[gate.decision] ?? tone.pass
  return (
    <Box className={`release-gate ${present.className}`} data-testid="release-gate">
      <Box className="release-gate-head">
        <Box className="release-gate-title">{present.icon}<Box><Typography className="eyebrow">RELEASE RISK GATE · 后端判定</Typography><Typography component="strong">{releaseGateLabels[gate.decision]}</Typography></Box></Box>
        <Box className="release-gate-stats">
          <Chip size="small" label={`断裂路径 ${gate.broken_path_count}`} />
          <Chip size="small" label={`关键服务断裂 ${gate.critical_failures}`} color={gate.critical_failures ? 'error' : 'default'} />
          <Chip size="small" label={`非关键断裂 ${gate.non_critical_failures}`} />
        </Box>
      </Box>
      <Alert severity={present.severity} icon={false}>{gate.summary}</Alert>
      {gate.decision === 'blocked_critical' && (
        <Box className="release-gate-blockers" data-testid="release-gate-blockers">
          <Typography className="eyebrow">受阻关键服务与断裂时刻</Typography>
          {gate.blocked_services.map((service) => (
            <Box className="release-gate-blocker" key={service.service_id}>
              <strong>{service.service_code}</strong>
              <span>{service.times.map((value) => formatDateTime(value)).join(' · ')}</span>
              {service.reasons.map((reason) => <Typography key={reason}>{reason}</Typography>)}
            </Box>
          ))}
        </Box>
      )}
      {simulated && gate.decision === 'risk_acceptance_required' && !riskAcceptance && (
        <Typography className="release-gate-hint">必须在下方填写并提交风险接受说明后，才能标记待执行；说明会随审计记录保留。</Typography>
      )}
      {riskAcceptance && (
        <Box className="release-gate-acceptance">
          <Typography className="eyebrow">已留存的风险接受说明</Typography>
          <Typography>{riskAcceptance}</Typography>
        </Box>
      )}
    </Box>
  )
}
