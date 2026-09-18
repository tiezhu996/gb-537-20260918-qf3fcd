// Release gate decisions are computed by the backend from the frozen
// simulation result. The UI must render these values, never infer them.
export const releaseGateDecisions = ['pass', 'risk_acceptance_required', 'blocked_critical'] as const
export type ReleaseGateDecision = (typeof releaseGateDecisions)[number]

export const releaseGateLabels: Record<ReleaseGateDecision, string> = {
  pass: '无断裂，直接放行',
  risk_acceptance_required: '仅非关键断裂，需风险接受',
  blocked_critical: '关键服务断裂，禁止放行',
}
