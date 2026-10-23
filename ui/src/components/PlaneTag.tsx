type Plane = 'control' | 'data'

export function PlaneTag({ plane }: { plane: Plane }) {
  return (
    <span className={`plane-tag plane-tag--${plane}`}>
      {plane === 'control' ? 'Control Plane' : 'Data Plane'}
    </span>
  )
}
