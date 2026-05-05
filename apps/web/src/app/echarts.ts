import * as echarts from 'echarts/core'
import { BarChart, RadarChart } from 'echarts/charts'
import { GridComponent, LegendComponent, RadarComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

// Register only what we use (tree-shake friendly)
echarts.use([BarChart, RadarChart, GridComponent, TooltipComponent, LegendComponent, RadarComponent, CanvasRenderer])

export { echarts }
