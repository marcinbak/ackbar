import test from 'node:test';
import assert from 'node:assert/strict';
import {
  getCanonicalTaskProject,
  getCanonicalTaskNodePath,
  matchesTaskProjectFilter,
  collectSelectableNodePaths,
  computeDefaultNewTaskNodePath,
} from './tasks-project.js';

test('getCanonicalTaskProject returns normalized project without group prefix', () => {
  assert.equal(getCanonicalTaskProject(null), 'General');
  assert.equal(getCanonicalTaskProject({}), 'General');
  assert.equal(getCanonicalTaskProject({ project_name: 'Ackbar' }), 'Ackbar');
  assert.equal(getCanonicalTaskProject({ group_name: 'Modemobile', project_name: 'Modemobile/NGL' }), 'NGL');
  assert.equal(getCanonicalTaskProject({ group_name: 'Modemobile', project_name: 'Modemobile/NGL/ngl-android' }), 'NGL/ngl-android');
  assert.equal(getCanonicalTaskProject({ group_name: 'Modemobile', project_name: 'NGL', subproject_name: 'ngl-android' }), 'NGL/ngl-android');
  assert.equal(getCanonicalTaskProject({ group_name: 'Modemobile', project_name: 'NGL/ngl-android', subproject_name: 'ngl-android' }), 'NGL/ngl-android');
});

test('getCanonicalTaskNodePath resolves full group/project path', () => {
  assert.equal(getCanonicalTaskNodePath(null), 'Personal/General');
  assert.equal(getCanonicalTaskNodePath({}), 'Personal/General');
  assert.equal(getCanonicalTaskNodePath({ group_name: 'Personal', project_name: 'Ackbar' }), 'Personal/Ackbar');
  assert.equal(getCanonicalTaskNodePath({ group_name: 'Modemobile', project_name: 'NGL/ngl-android' }), 'Modemobile/NGL/ngl-android');
  assert.equal(getCanonicalTaskNodePath({ group_name: 'Modemobile', project_name: 'NGL', subproject_name: 'ngl-android' }), 'Modemobile/NGL/ngl-android');
});

test('matchesTaskProjectFilter handles hierarchical and legacy project filters', () => {
  const taskParent = { group_name: 'Modemobile', project_name: 'NGL' };
  const taskChild = { group_name: 'Modemobile', project_name: 'NGL/ngl-android' };
  const taskLegacy = { group_name: 'Modemobile', project_name: 'NGL', subproject_name: 'ngl-android' };
  const taskOther = { group_name: 'Modemobile', project_name: 'Due-Dilligence' };
  const taskPersonal = { group_name: 'Personal', project_name: 'Ackbar' };

  // Group filter matches
  assert.equal(matchesTaskProjectFilter(taskParent, 'Modemobile', 'all'), true);
  assert.equal(matchesTaskProjectFilter(taskPersonal, 'Modemobile', 'all'), false);
  assert.equal(matchesTaskProjectFilter(taskPersonal, 'all', 'all'), true);

  // Hierarchical parent project filter matches both parent and children
  assert.equal(matchesTaskProjectFilter(taskParent, 'Modemobile', 'NGL'), true);
  assert.equal(matchesTaskProjectFilter(taskChild, 'Modemobile', 'NGL'), true);
  assert.equal(matchesTaskProjectFilter(taskLegacy, 'Modemobile', 'NGL'), true);
  assert.equal(matchesTaskProjectFilter(taskOther, 'Modemobile', 'NGL'), false);

  // Specific leaf project filter matches child and legacy child, but not parent
  assert.equal(matchesTaskProjectFilter(taskParent, 'Modemobile', 'NGL/ngl-android'), false);
  assert.equal(matchesTaskProjectFilter(taskChild, 'Modemobile', 'NGL/ngl-android'), true);
  assert.equal(matchesTaskProjectFilter(taskLegacy, 'Modemobile', 'NGL/ngl-android'), true);
});

test('collectSelectableNodePaths extracts sorted unique paths from treeNodes and tasks', () => {
  const treeNodes = [
    { path: 'Modemobile/Due-Dilligence' },
    { path: 'Modemobile/NGL' },
    { path: 'Modemobile/NGL/ngl-android' },
    { path: 'Unassigned' },
    { path: '' },
  ];
  const tasks = [
    { group_name: 'Personal', project_name: 'Ackbar' },
    { group_name: 'Modemobile', project_name: 'NGL', subproject_name: 'ngl-android' },
  ];

  const paths = collectSelectableNodePaths(treeNodes, tasks);
  assert.deepEqual(paths, [
    'Modemobile/Due-Dilligence',
    'Modemobile/NGL',
    'Modemobile/NGL/ngl-android',
    'Personal/Ackbar',
  ]);
});

test('computeDefaultNewTaskNodePath respects unambiguous choices and avoids false positives', () => {
  const nodePaths = [
    'Modemobile/Due-Dilligence',
    'Modemobile/NGL',
    'Modemobile/NGL/ngl-android',
    'Personal/Ackbar',
  ];

  // Specific group and project match
  assert.equal(computeDefaultNewTaskNodePath(nodePaths, 'Modemobile', 'NGL'), 'Modemobile/NGL');
  assert.equal(computeDefaultNewTaskNodePath(nodePaths, 'Modemobile', 'NGL/ngl-android'), 'Modemobile/NGL/ngl-android');

  // Multiple project matches under group must NOT silently choose first path
  assert.equal(computeDefaultNewTaskNodePath(nodePaths, 'Modemobile', 'all'), '');

  // Exactly one project match under group auto-selects
  const singleGroupPaths = ['Personal/Ackbar', 'Modemobile/Due-Dilligence'];
  assert.equal(computeDefaultNewTaskNodePath(singleGroupPaths, 'Personal', 'all'), 'Personal/Ackbar');

  // All work filter with multiple paths returns empty
  assert.equal(computeDefaultNewTaskNodePath(nodePaths, 'all', 'all'), '');
});
