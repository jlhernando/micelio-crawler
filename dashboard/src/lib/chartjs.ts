/**
 * Chart.js with the dashboard's defaults and the controllers it uses.
 * Components load it with `await import('…/chartjs')` so chart.js stays out
 * of the main bundle and only downloads when a chart is shown.
 */
import {
  Chart,
  ArcElement,
  BarController,
  BarElement,
  CategoryScale,
  DoughnutController,
  Filler,
  Legend,
  LinearScale,
  Tooltip,
} from 'chart.js';
import { C } from './palette';

Chart.register(DoughnutController, ArcElement, BarController, BarElement, CategoryScale, LinearScale, Tooltip, Legend, Filler);

Chart.defaults.color = C.fg2;
Chart.defaults.borderColor = C.border;
Chart.defaults.font.family = '-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif';
Chart.defaults.font.size = 11;

export { Chart };
